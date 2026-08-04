package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

const (
	defaultReplicationTimeout = 10 * time.Minute
	replicationLockName       = "replicate.lock"
	// External tools can name private attachment files in their diagnostics.
	// Keep those bytes bounded so a failure message stays a failure message.
	maxReplicationCommandOutputBytes = 64 << 10
)

type ReplicateCmd struct {
	Destination string        `name:"to" required:"" help:"Replica state root as USER@HOST:/absolute/path"`
	Timeout     time.Duration `name:"timeout" default:"10m" help:"Maximum time for the complete replication"`
	Args        []string      `arg:"" optional:"" name:"trawler" help:"Trawler names; all installed trawlers when omitted"`
}

func (c *ReplicateCmd) Run(r *Runtime) error {
	destination, err := parseReplicationDestination(c.Destination)
	if err != nil {
		return usageErr{err}
	}
	trawlers, err := r.selectedTrawlerArguments(c.Args)
	if err != nil {
		return err
	}
	trawlers = canonicalUpdateTrawlers(trawlers)
	if len(trawlers) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No trawlers found.")
		return err
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultReplicationTimeout
	}
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()

	// Replication reads archives that an update would be rewriting. Both take
	// the same kind of exclusive state-root lock so the two never overlap.
	lock, err := acquireReplicationLock(r.stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	replicator := archiveReplicator{
		commands: r.replicationCommandRunner(),
		locate:   r.trawlerExecutor().ResolveTrawlerArchiveLocation,
		stderr:   r.lockedStderr(),
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		return replicationError{
			code:    "no_archives",
			message: "None of the selected trawlers has an archive to replicate. Run update first.",
		}
	}
	if err := replicator.preflight(ctx, destination, archives); err != nil {
		return err
	}
	replicated, err := replicator.replicate(ctx, destination, archives, remoteDirs)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		r.stdout,
		"Replicated %s to %s.\n",
		strings.Join(replicated, ", "),
		destination.String(),
	)
	return err
}

type replicationDestination struct {
	host string
	root string
}

func (d replicationDestination) String() string { return d.host + ":" + d.root }

// parseReplicationDestination accepts only a simple SSH host and a safe
// absolute path. Both halves reach argv of ssh and sqlite3_rsync, so anything
// that could be read as an option or a shell construct is rejected here rather
// than quoted later.
func parseReplicationDestination(value string) (replicationDestination, error) {
	host, root, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || !validSSHHost(host) {
		return replicationDestination{}, errors.New("--to must be USER@HOST:/absolute/path using a simple SSH host or alias")
	}
	if root == "/" || !strings.HasPrefix(root, "/") || path.Clean(root) != root || !validRemotePath(root) {
		return replicationDestination{}, errors.New("--to requires a safe absolute remote state-root path, not / itself")
	}
	return replicationDestination{host: host, root: root}, nil
}

func validSSHHost(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Count(value, "@") > 1 {
		return false
	}
	for _, part := range strings.Split(value, "@") {
		if part == "" || strings.HasPrefix(part, "-") {
			return false
		}
	}
	return onlyContainsRunes(value, "._-@")
}

func validRemotePath(value string) bool {
	return onlyContainsRunes(value, "/._-")
}

func onlyContainsRunes(value, extra string) bool {
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(extra, r) {
			continue
		}
		return false
	}
	return true
}

type archiveReplicator struct {
	commands replicationCommandRunner
	locate   func(trawlkit.Trawler) (trawlkit.ResolvedTrawlerArchiveLocation, error)
	stderr   io.Writer
}

// preflight fails before anything is copied. A replication that dies halfway
// leaves the replica holding a mix of two archives, so a missing dependency has
// to be found while the replica is still untouched.
func (a archiveReplicator) preflight(ctx context.Context, destination replicationDestination, archives []replicationArchive) error {
	required := []string{"ssh", "sqlite3_rsync"}
	if anyArchiveCarriesAttachments(archives) {
		required = append(required, "rsync")
	}
	for _, executable := range required {
		if _, err := a.commands.LookPath(executable); err != nil {
			return replicationError{
				code:    "dependency_missing",
				message: executable + " is required for replication. Install it on this Mac and retry.",
			}
		}
	}
	remoteRequired := []string{"sqlite3_rsync", "sqlite3"}
	for _, executable := range remoteRequired {
		if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "command", "-v", executable); err != nil {
			return replicationError{
				code:    "remote_dependency_missing",
				message: executable + " is not available on the replica host. Install a current SQLite sqlite3 and sqlite3_rsync there and retry.",
			}
		}
	}
	return nil
}

func (a archiveReplicator) replicate(
	ctx context.Context,
	destination replicationDestination,
	archives []replicationArchive,
	remoteDirs []string,
) ([]string, error) {
	if _, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "mkdir", "-p", "--"}, remoteDirs...)...); err != nil {
		return nil, replicationCommandError("prepare remote state root", err)
	}
	if _, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "chmod", "700", "--"}, remoteDirs...)...); err != nil {
		return nil, replicationCommandError("protect remote state root", err)
	}
	replicated := make([]string, 0, len(archives))
	for _, archive := range archives {
		_, _ = fmt.Fprintf(a.stderr, "%s replicating…\n", archive.name)
		// Attachments first. An archive records attachment paths relative to
		// its own directory, so a database that arrived before its files would
		// point at names that are not there yet. Stale remote attachments are
		// harmless and are kept: replication never deletes on the replica.
		if archive.localAttachments != "" {
			if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "mkdir", "-p", "--", archive.remoteAttachments); err != nil {
				return nil, replicationCommandError("prepare the "+archive.name+" attachments", err)
			}
			if _, err := a.commands.Run(ctx, "rsync", "-a", "--",
				archive.localAttachments+string(filepath.Separator),
				destination.host+":"+archive.remoteAttachments+"/",
			); err != nil {
				return nil, replicationCommandError("replicate the "+archive.name+" attachments", err)
			}
		}
		if _, err := a.commands.Run(ctx, "sqlite3_rsync", archive.local, destination.host+":"+archive.remote); err != nil {
			return nil, replicationCommandError("replicate "+archive.name, err)
		}
		if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "chmod", "600", "--", archive.remote); err != nil {
			return nil, replicationCommandError("protect the "+archive.name+" replica", err)
		}
		// A replica that arrived corrupt is worse than no replica, because it
		// reads as a successful copy. Validate before calling this one done.
		// ssh does not preserve argument boundaries: it joins its arguments with
		// spaces and hands the remote shell one string to re-split. Passing the
		// statement as its own argument therefore reaches sqlite3 as two — it
		// runs "PRAGMA" on its own, which is incomplete SQL, and exits 1. The
		// remote command has to be built as a single argument, quoted here
		// rather than by a shell that never sees the boundary.
		//
		// archive.remote needs no quoting of its own: validRemotePath restricts
		// it to alphanumerics plus "/._-", so it cannot carry whitespace.
		remoteCommand := "sqlite3 -readonly " + archive.remote + " 'PRAGMA quick_check;'"
		output, err := a.commands.Run(ctx, "ssh", "--", destination.host, remoteCommand)
		if err != nil {
			return nil, replicationCommandError("validate the "+archive.name+" replica", err)
		}
		if strings.TrimSpace(output) != "ok" {
			return nil, replicationError{
				code:    "replica_invalid",
				message: "The " + archive.name + " replica failed SQLite integrity validation. The previous replica is kept; inspect the replica host storage and retry.",
			}
		}
		replicated = append(replicated, archive.name)
	}
	return replicated, nil
}

// plan resolves every archive before copying any of them, so a trawler with an
// unportable path fails the whole run rather than leaving a partial replica.
func (a archiveReplicator) plan(
	destination replicationDestination,
	trawlers []InstalledTrawler,
) ([]replicationArchive, []string, error) {
	archives := make([]replicationArchive, 0, len(trawlers))
	remoteDirs := []string{destination.root}
	for _, trawler := range trawlers {
		if trawler.TrawlerDiscoveryError != nil || trawler.Trawler == nil {
			continue
		}
		located, err := a.locate(trawler.Trawler)
		if err != nil {
			return nil, nil, replicationError{
				code:    "archive_path_invalid",
				message: "The " + trawlerHumanName(trawler) + " archive path could not be resolved.",
			}
		}
		remoteRelative, err := portableRemoteRelativePath(located)
		if err != nil {
			return nil, nil, replicationError{
				code:    "archive_not_portable",
				message: "The " + trawlerHumanName(trawler) + " archive is not under the state root, so it has no place in the replica. Move it under OPENTRAWL_STATE_ROOT and retry.",
			}
		}
		// A trawler that has never been updated has no archive. That is not a
		// failure: replicate what exists.
		info, err := os.Stat(located.TrawlerArchivePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		archive := replicationArchive{
			name:   trawlerCommandToken(trawler),
			local:  located.TrawlerArchivePath,
			remote: path.Join(destination.root, remoteRelative),
		}
		localAttachments := filepath.Join(filepath.Dir(archive.local), attachmentsDirName)
		if info, err := os.Stat(localAttachments); err == nil && info.IsDir() {
			archive.localAttachments = localAttachments
			archive.remoteAttachments = path.Join(path.Dir(archive.remote), attachmentsDirName)
		}
		archives = append(archives, archive)
		remoteDirs = append(remoteDirs, path.Dir(archive.remote))
	}
	return archives, remoteDirs, nil
}

func portableRemoteRelativePath(located trawlkit.ResolvedTrawlerArchiveLocation) (string, error) {
	relative, err := filepath.Rel(located.StateRoot, located.TrawlerArchivePath)
	if err != nil {
		return "", err
	}
	remoteRelative := filepath.ToSlash(relative)
	escapesStateRoot := relative == "." ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative)
	if escapesStateRoot || !validRemotePath("/"+remoteRelative) {
		return "", errors.New("archive path is not portable to the replica state root")
	}
	return remoteRelative, nil
}

type replicationArchive struct {
	name              string
	local             string
	remote            string
	localAttachments  string
	remoteAttachments string
}

// attachmentsDirName is the directory a trawler writes beside its archive for
// files the database only references by path. Notes uses one for every image
// and document in a note.
const attachmentsDirName = "attachments"

func anyArchiveCarriesAttachments(archives []replicationArchive) bool {
	for _, archive := range archives {
		if archive.localAttachments != "" {
			return true
		}
	}
	return false
}

type replicationCommandRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}

func (r *Runtime) replicationCommandRunner() replicationCommandRunner {
	if r.replicationRunner != nil {
		return r.replicationRunner
	}
	return execReplicationCommandRunner{}
}

type execReplicationCommandRunner struct{}

func (execReplicationCommandRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (execReplicationCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- executable names are fixed; user values are validated by parseReplicationDestination and passed as argv, never through a shell.
	var output boundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return output.String(), replicationError{
				code:    "replication_timeout",
				message: "Replication timed out. Check the replica host connection and retry with a larger --timeout.",
			}
		}
		return output.String(), fmt.Errorf("%s: %w", name, err)
	}
	return output.String(), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if remaining := maxReplicationCommandOutputBytes - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return original, nil
}

type replicationError struct {
	code    string
	message string
}

func (e replicationError) Error() string { return e.message }

func (e replicationError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{Code: e.code, Message: e.message}
}

func replicationCommandError(action string, err error) error {
	var typed replicationError
	if errors.As(err, &typed) {
		return err
	}
	return replicationError{
		code:    "replication_failed",
		message: "Could not " + action + ". Check the SSH connection and the replica host, then retry: " + err.Error(),
	}
}

type replicationLock struct {
	file *os.File
}

func acquireReplicationLock(stateRoot string) (*replicationLock, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create OpenTrawl state: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(root, replicationLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open replicate lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, replicationAlreadyRunningError{}
		}
		return nil, fmt.Errorf("lock replicate: %w", err)
	}
	return &replicationLock{file: file}, nil
}

func (lock *replicationLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return lock.file.Close()
}

type replicationAlreadyRunningError struct{}

func (replicationAlreadyRunningError) Error() string { return "OpenTrawl is already replicating." }

func (replicationAlreadyRunningError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    "already_replicating",
		Message: "OpenTrawl is already replicating.",
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	defaultReplicationTimeout = 10 * time.Minute
	replicationLockName       = "replicate.lock"
	// External tools can name private attachment files in their diagnostics.
	// Keep those bytes bounded so a failure message stays a failure message.
	maxReplicationCommandOutputBytes = 64 << 10
	// A failure message is read in a terminal, so it carries only the first
	// part of what the command said. The full output is bounded separately
	// above.
	maxReplicationCommandDetailRunes = 300
)

type ReplicateCmd struct {
	Destination string        `name:"to" required:"" placeholder:"USER@HOST:/PATH" help:"Replica state root as USER@HOST:/absolute/path"`
	Timeout     time.Duration `name:"timeout" default:"10m" placeholder:"DURATION" help:"Maximum time for the complete replication"`
	Args        []string      `arg:"" optional:"" name:"trawler" help:"Trawler names; all installed trawlers when omitted"`
}

func (c *ReplicateCmd) Run(r *Runtime) error {
	destination, err := parseReplicationDestination(c.Destination)
	if err != nil {
		return err
	}
	trawlers, err := r.selectedTrawlerArguments(c.Args)
	if err != nil {
		return err
	}
	trawlers = canonicalUpdateTrawlers(trawlers)
	if len(trawlers) == 0 {
		return render.NoTrawlersAreAvailable()
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
		output:   r.output,
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		return render.NoSelectedTrawlerHasArchiveToReplicate()
	}
	if err := replicator.preflight(ctx, destination, archives); err != nil {
		return err
	}
	replicated, err := replicator.replicate(ctx, destination, archives, remoteDirs)
	if err != nil {
		return err
	}
	return r.output.WriteArchiveReplication(replicated, destination.String())
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
		return replicationDestination{}, render.ReplicaDestinationMustBeHostAndAbsolutePath()
	}
	if root == "/" || !strings.HasPrefix(root, "/") || path.Clean(root) != root || !validRemotePath(root) {
		return replicationDestination{}, render.ReplicaDestinationMustBeHostAndAbsolutePath()
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
	output   render.TrawlCommandOutput
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
			return render.ReplicationNeedsProgramOnThisComputer(executable)
		}
	}
	remoteRequired := []string{"sqlite3_rsync", "sqlite3"}
	for _, executable := range remoteRequired {
		if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "command", "-v", executable); err != nil {
			return render.ReplicationNeedsProgramOnReplicaHost(executable)
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
	if output, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "mkdir", "-p", "--"}, remoteDirs...)...); err != nil {
		return nil, replicationCommandError("prepare remote state root", output, err)
	}
	// 750, not 700. The replica holds the same personal data as the Mac and
	// must never be readable by the world, but a replica that grants a reader
	// access through a POSIX ACL keeps that grant in the group bits: they are
	// the ACL mask, so 700 would clamp every entry the host granted down to
	// nothing on every run. 750 is the widest a reader of this tree is ever
	// allowed — enter and list, never write — and "other" still gets nothing.
	if output, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "chmod", "750", "--"}, remoteDirs...)...); err != nil {
		return nil, replicationCommandError("protect remote state root", output, err)
	}
	replicated := make([]string, 0, len(archives))
	for _, archive := range archives {
		if err := a.output.WriteArchiveReplicationStarted(archive.name); err != nil {
			return nil, err
		}
		// Attachments first. An archive records attachment paths relative to
		// its own directory, so a database that arrived before its files would
		// point at names that are not there yet. Stale remote attachments are
		// harmless and are kept: replication never deletes on the replica.
		if archive.localAttachments != "" {
			if output, err := a.commands.Run(ctx, "ssh", "--", destination.host, "mkdir", "-p", "--", archive.remoteAttachments); err != nil {
				return nil, replicationCommandError("prepare the "+archive.name+" attachments", output, err)
			}
			if output, err := a.commands.Run(ctx, "rsync", "-a", "--",
				archive.localAttachments+string(filepath.Separator),
				destination.host+":"+archive.remoteAttachments+"/",
			); err != nil {
				return nil, replicationCommandError("replicate the "+archive.name+" attachments", output, err)
			}
		}
		if output, err := a.commands.Run(ctx, "sqlite3_rsync", archive.local, destination.host+":"+archive.remote); err != nil {
			return nil, replicationCommandError("replicate "+archive.name, output, err)
		}
		// 660 rather than 640: on a replica that grants its reader an ACL the
		// group bits are the mask, and SQLite fchmods a zero-length -wal to the
		// archive's own mode on every open by the file's owner, so a 640
		// archive snapped the -wal's mask back to r-- each time root ran the
		// quick_check below and undid the 660 the sidecars are given after it.
		// Group and other grant nothing on the replica, so 660 widens nothing
		// by itself: the archive's user:reader:r-- entry keeps the reader
		// read-only, and the -wal now inherits a rw- mask from the archive.
		if output, err := a.commands.Run(ctx, "ssh", "--", destination.host, "chmod", "660", "--", archive.remote); err != nil {
			return nil, replicationCommandError("protect the "+archive.name+" replica", output, err)
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
			return nil, replicationCommandError("validate the "+archive.name+" replica", output, err)
		}
		if strings.TrimSpace(output) != "ok" {
			return nil, render.ReplicaFailedIntegrityCheck(archive.name)
		}
		if output, err := a.commands.Run(ctx, "ssh", "--", destination.host, sidecarPermissionCommand(archive.remote)); err != nil {
			return nil, replicationCommandError("open the "+archive.name+" replica sidecars to its reader", output, err)
		}
		replicated = append(replicated, archive.name)
	}
	return replicated, nil
}

// replicaSidecarSuffixes name the files beside an archive that a reader of the
// replica has to write even though it only reads the archive: SQLite's
// write-ahead log and shared-memory index, and the lock that guards the
// archive's file set.
var replicaSidecarSuffixes = []string{"-wal", "-shm", store.TrawlerArchiveFileSetLockSuffix}

// sidecarPermissionCommand gives the sidecars group rw, and leaves the archive
// itself at the 660 it was given above.
//
// Reading a WAL database is a writing act: the reader extends the -wal, maps
// the -shm and takes the file-set lock. Where the replica grants that reader
// access through a POSIX ACL, a file's group bits are its ACL mask, so a
// sidecar that this run created 0600 clamps a user:reader:rw- entry down to an
// effective r-- and the reader cannot open the archive at all until someone
// repairs the mask by hand. Group rw is what makes the mask land right the
// first time. SQLite already re-fchmods a zero-length -wal to the archive's
// mode on every open by its owner, so with the archive at 660 the -wal and
// -shm land at rw- on their own; this command still covers the lock file,
// which SQLite never touches, and hosts that grant nothing through an ACL.
// The archive is not named here: 660 on it is a ceiling, and the reader's own
// user:reader:r-- entry is what keeps the archive read-only to it.
//
// A sidecar exists only while the replica is mid-WAL and the lock file only
// once the replica's reader has run, so a missing one is ordinary rather than
// a failure, and the command skips it. A chmod that genuinely fails still
// fails the run.
//
// The whole thing goes over as a single argument: ssh joins its arguments with
// spaces and hands the remote shell one string to re-split, so a command built
// as argv would arrive taken apart. The paths need no quoting of their own —
// validRemotePath restricts them to alphanumerics plus "/._-" — but they are
// quoted anyway, because the shell that reads this is not the one that
// validated them.
func sidecarPermissionCommand(remoteArchive string) string {
	sidecars := make([]string, 0, len(replicaSidecarSuffixes))
	for _, suffix := range replicaSidecarSuffixes {
		sidecars = append(sidecars, remoteArchive+suffix)
	}
	return "for sidecar in " + strings.Join(sidecars, " ") +
		"; do if [ -e \"$sidecar\" ]; then chmod 660 -- \"$sidecar\"; fi; done"
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
			return nil, nil, render.TrawlerArchiveLocationIsUnknown(trawlerHumanName(trawler))
		}
		remoteRelative, err := portableRemoteRelativePath(located)
		if err != nil {
			return nil, nil, render.TrawlerArchiveIsOutsideStateRoot(trawlerHumanName(trawler))
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
			return output.String(), render.ReplicationTimedOut()
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

// replicationStepFailure shows the reader one plain sentence and keeps what
// the failed program said for the run log.
type replicationStepFailure struct {
	replicationStepCommandFailure render.CommandFailure
	failedProgramReport           string
}

func (failure replicationStepFailure) Error() string {
	return failure.replicationStepCommandFailure.Error()
}

func (failure replicationStepFailure) Unwrap() error {
	return failure.replicationStepCommandFailure
}

func (failure replicationStepFailure) InternalErrorLogMessage() string {
	return failure.replicationStepCommandFailure.Error() + " " + failure.failedProgramReport
}

func replicationCommandError(replicationStep string, output string, err error) error {
	if _, worded := render.AsCommandFailure(err); worded {
		return err
	}
	failedProgramReport := err.Error()
	if detail := commandFailureDetail(output); detail != "" {
		failedProgramReport += ": " + detail
	}
	return replicationStepFailure{
		replicationStepCommandFailure: render.ReplicationStepFailed(replicationStep),
		failedProgramReport:           failedProgramReport,
	}
}

// commandFailureDetail carries what the failed command actually said. ssh
// reports only its own exit status, so without this a remote failure reads as
// a connection problem whatever went wrong on the other side — a broken SQL
// statement and an unreachable host produce the same "ssh: exit status 1".
//
// The runner already bounds what it collects, because external tools can name
// private attachment files in their diagnostics. This bounds it further: a
// failure message has to stay a message, so it is one line and short enough to
// read.
func commandFailureDetail(output string) string {
	detail := strings.Join(strings.Fields(output), " ")
	if detail == "" {
		return ""
	}
	if runes := []rune(detail); len(runes) > maxReplicationCommandDetailRunes {
		detail = string(runes[:maxReplicationCommandDetailRunes]) + "…"
	}
	return detail
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
			return nil, render.ReplicationIsAlreadyRunning()
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

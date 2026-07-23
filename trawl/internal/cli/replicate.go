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
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

const (
	defaultReplicationTimeout = 10 * time.Minute
	maxCommandOutputBytes     = 64 << 10
)

type ReplicateCmd struct {
	Destination string        `name:"to" required:"" help:"Remote state root as USER@HOST:/absolute/path"`
	Timeout     time.Duration `name:"timeout" default:"10m" help:"Maximum time for the complete replication"`
	Sources     []string      `arg:"" name:"source" help:"One or more source ids to replicate"`
}

type ReplicateResult struct {
	Event       string   `json:"event"`
	State       string   `json:"state"`
	Destination string   `json:"destination"`
	Sources     []string `json:"sources"`
}

func (c *ReplicateCmd) Run(r *Runtime) error {
	destination, err := parseReplicationDestination(c.Destination)
	if err != nil {
		return usageErr{err}
	}
	if len(c.Sources) == 0 {
		return usageErr{errors.New("replicate requires at least one source")}
	}
	sources, err := r.selectedSourceArgs(c.Sources)
	if err != nil {
		return err
	}
	sources = canonicalSyncSources(sources)

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultReplicationTimeout
	}
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	replicator := archiveReplicator{
		commands: r.replicationCommandRunner(),
		paths: trawlkit.NewSourceExecutor(trawlkit.SourceExecutorOptions{
			StateRoot: r.stateRoot,
		}).Paths,
		stderr: r.lockedStderr(),
	}
	lock, err := acquireReplicationLock(r.stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	if err := replicator.preflight(ctx, destination, sources); err != nil {
		return err
	}
	if err := replicator.replicate(ctx, destination, sources); err != nil {
		return err
	}
	result := ReplicateResult{
		Event:       "replicate",
		State:       "ok",
		Destination: destination.String(),
		Sources:     sourceIDs(sources),
	}
	if r.root.JSON {
		return writeJSON(r.stdout, result)
	}
	_, err = fmt.Fprintf(r.stdout, "Replicated %s to %s.\n", strings.Join(result.Sources, ", "), result.Destination)
	return err
}

type replicationDestination struct {
	host string
	root string
}

func (d replicationDestination) String() string { return d.host + ":" + d.root }

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
	parts := strings.Split(value, "@")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "-") {
			return false
		}
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r) {
			continue
		}
		return false
	}
	return true
}

func validRemotePath(value string) bool {
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r) {
			continue
		}
		return false
	}
	return true
}

type archiveReplicator struct {
	commands replicationCommandRunner
	paths    func(trawlkit.Crawler) (trawlkit.SourcePaths, error)
	stderr   io.Writer
}

func (a archiveReplicator) preflight(ctx context.Context, destination replicationDestination, sources []Source) error {
	for _, executable := range replicationExecutables(sources) {
		if _, err := a.commands.LookPath(executable); err != nil {
			return replicationError{
				code:    "dependency_missing",
				message: executable + " is required for replication",
				remedy:  "install " + executable + " on this Mac and retry",
			}
		}
	}
	for _, executable := range []string{"sqlite3_rsync", "sqlite3"} {
		if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "command", "-v", executable); err != nil {
			return replicationError{
				code:    "remote_dependency_missing",
				message: executable + " is not available on the replica host",
				remedy:  "install a current SQLite sqlite3 and sqlite3_rsync on the VPS and retry",
			}
		}
	}
	return nil
}

func replicationExecutables(sources []Source) []string {
	executables := []string{"ssh", "sqlite3_rsync"}
	for _, source := range sources {
		if source.ID == "notes" {
			return append(executables, "rsync")
		}
	}
	return executables
}

func (a archiveReplicator) replicate(ctx context.Context, destination replicationDestination, sources []Source) error {
	archives := make([]replicationArchive, 0, len(sources))
	remoteDirs := []string{destination.root}
	for _, source := range sources {
		resolved, err := a.paths(source.Crawler)
		if err != nil {
			return replicationError{
				code:    "archive_path_invalid",
				message: sourceHumanName(source) + " archive path could not be resolved",
				remedy:  "inspect the source path configuration and retry",
			}
		}
		relative, err := filepath.Rel(resolved.StateRoot, resolved.Archive)
		remoteRelative := filepath.ToSlash(relative)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || !validRemotePath("/"+remoteRelative) {
			return replicationError{
				code:    "archive_not_portable",
				message: sourceHumanName(source) + " archive path is not portable to the replica state root",
				remedy:  "place the archive at a simple path under OPENTRAWL_STATE_ROOT before replicating it",
			}
		}
		archive := replicationArchive{
			source: source,
			local:  resolved.Archive,
			remote: path.Join(destination.root, remoteRelative),
		}
		info, err := os.Stat(archive.local)
		if err != nil || !info.Mode().IsRegular() {
			return replicationError{
				code:    "archive_missing",
				message: sourceHumanName(source) + " has no local archive to replicate",
				remedy:  "run trawl sync " + source.ID + " and retry",
			}
		}
		archives = append(archives, archive)
		remoteDirs = append(remoteDirs, path.Dir(archive.remote))
	}
	if _, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "mkdir", "-p", "--"}, remoteDirs...)...); err != nil {
		return replicationCommandError("prepare remote state root", err)
	}
	if _, err := a.commands.Run(ctx, "ssh", append([]string{"--", destination.host, "chmod", "700", "--"}, remoteDirs...)...); err != nil {
		return replicationCommandError("protect remote state root", err)
	}

	// Notes records attachment paths relative to the archive directory. Copy
	// attachments first so the newly replicated database never points at a
	// file that has not arrived yet. Stale remote attachments are harmless and
	// are retained; replication never performs a remote deletion.
	if notes, ok := replicationArchiveForSource(archives, "notes"); ok {
		localAttachments := filepath.Join(filepath.Dir(notes.local), "attachments")
		if info, err := os.Stat(localAttachments); err == nil && info.IsDir() {
			remoteAttachments := path.Join(path.Dir(notes.remote), "attachments")
			if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "mkdir", "-p", "--", remoteAttachments); err != nil {
				return replicationCommandError("prepare Notes attachments", err)
			}
			if _, err := a.commands.Run(ctx, "rsync", "-a", "--", localAttachments+string(filepath.Separator), destination.host+":"+remoteAttachments+"/"); err != nil {
				return replicationCommandError("replicate Notes attachments", err)
			}
		}
	}

	for _, archive := range archives {
		_, _ = fmt.Fprintf(a.stderr, "%s replicating…\n", sourceHumanName(archive.source))
		if _, err := a.commands.Run(ctx, "sqlite3_rsync", archive.local, destination.host+":"+archive.remote); err != nil {
			return replicationCommandError("replicate "+archive.source.ID, err)
		}
		if _, err := a.commands.Run(ctx, "ssh", "--", destination.host, "chmod", "600", "--", archive.remote); err != nil {
			return replicationCommandError("protect "+archive.source.ID+" replica", err)
		}
		output, err := a.commands.Run(ctx, "ssh", "--", destination.host, "sqlite3", "-readonly", archive.remote, "'PRAGMA quick_check;'")
		if err != nil {
			return replicationCommandError("validate "+archive.source.ID+" replica", err)
		}
		if strings.TrimSpace(output) != "ok" {
			return replicationError{
				code:    "replica_invalid",
				message: archive.source.ID + " replica failed SQLite integrity validation",
				remedy:  "keep the previous replica, inspect the VPS storage, and retry",
			}
		}
	}
	return nil
}

type replicationArchive struct {
	source Source
	local  string
	remote string
}

func replicationArchiveForSource(archives []replicationArchive, id string) (replicationArchive, bool) {
	for _, archive := range archives {
		if archive.source.ID == id {
			return archive, true
		}
	}
	return replicationArchive{}, false
}

func sourceIDs(sources []Source) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return ids
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
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- executable names are fixed; user values are validated and passed as argv.
	var output boundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return output.String(), replicationError{code: "replication_timeout", message: "replication timed out", remedy: "check the VPS connection and retry with a larger --timeout"}
		}
		// External tools can include private attachment filenames in their
		// diagnostics. Keep those bytes bounded for the command protocol but
		// never promote them into normal human output or the trawl log.
		return output.String(), fmt.Errorf("%s: %w", name, err)
	}
	return output.String(), nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := maxCommandOutputBytes - b.Len()
	if remaining > 0 {
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
	remedy  string
}

func (e replicationError) Error() string { return e.message }

func (e replicationError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{Code: e.code, Message: e.message, Remedy: e.remedy}
}

func replicationCommandError(action string, err error) error {
	var typed replicationError
	if errors.As(err, &typed) {
		return err
	}
	return replicationError{
		code:    "replication_failed",
		message: action + " failed",
		remedy:  "check the SSH connection and replica host, then retry: " + err.Error(),
	}
}

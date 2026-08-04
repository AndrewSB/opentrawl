package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
)

// The destination reaches argv of ssh and sqlite3_rsync. Everything that could
// be read as an option, a shell construct, or a path outside the replica state
// root has to be refused here rather than quoted later.
func TestReplicationDestinationRefusesUnsafeValues(t *testing.T) {
	for _, unsafe := range []string{
		"",
		"host",
		"host:",
		"host:relative/path",
		"host:/",
		"host:/root/../etc",
		"host:/root; rm -rf /",
		"host:/root$(whoami)",
		"host:/root with space",
		"-oProxyCommand=x:/root",
		"user@-h:/root",
		"user@@host:/root",
		"ho st:/root",
		"host:/root\n/etc",
	} {
		if _, err := parseReplicationDestination(unsafe); err == nil {
			t.Errorf("accepted unsafe destination %q", unsafe)
		}
	}
}

func TestReplicationDestinationAcceptsOrdinaryHosts(t *testing.T) {
	for _, value := range []string{
		"host:/srv/opentrawl",
		"user@host:/srv/opentrawl",
		"user@host.example.com:/srv/opentrawl",
		"vps-alias:/var/lib/opentrawl-replica",
	} {
		destination, err := parseReplicationDestination(value)
		if err != nil {
			t.Errorf("rejected ordinary destination %q: %v", value, err)
			continue
		}
		if destination.String() != value {
			t.Errorf("destination %q round-tripped as %q", value, destination.String())
		}
	}
}

// An archive outside the state root has no place in the replica: the replica is
// a state root, and there is nowhere to put a file that is not under one.
func TestPortableRemoteRelativePathRejectsArchivesOutsideTheStateRoot(t *testing.T) {
	for _, located := range []trawlkit.ResolvedTrawlerArchiveLocation{
		{StateRoot: "/state", TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{TrawlerArchivePath: "/elsewhere/a.db"}},
		{StateRoot: "/state", TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{TrawlerArchivePath: "/state"}},
		{StateRoot: "/state", TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{TrawlerArchivePath: "/state/a b.db"}},
	} {
		if _, err := portableRemoteRelativePath(located); err == nil {
			t.Errorf("accepted unportable archive path %q under %q",
				located.TrawlerArchivePath, located.StateRoot)
		}
	}
	located := trawlkit.ResolvedTrawlerArchiveLocation{
		StateRoot:           "/state",
		TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{TrawlerArchivePath: "/state/whatsapp/whatsapp.db"},
	}
	relative, err := portableRemoteRelativePath(located)
	if err != nil {
		t.Fatal(err)
	}
	if relative != "whatsapp/whatsapp.db" {
		t.Fatalf("unexpected remote relative path %q", relative)
	}
}

type recordedCommand struct {
	name string
	args []string
}

type fakeReplicationRunner struct {
	missingLocal map[string]bool
	commands     []recordedCommand
	responses    map[string]string
	failures     map[string]error
}

func (f *fakeReplicationRunner) LookPath(name string) (string, error) {
	if f.missingLocal[name] {
		return "", errors.New("not found")
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeReplicationRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.commands = append(f.commands, recordedCommand{name: name, args: args})
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if err, failed := f.failures[key]; failed {
		return "", err
	}
	if response, known := f.responses[key]; known {
		return response, nil
	}
	// The validation command is one argument, because ssh does not preserve
	// argument boundaries; match it by prefix rather than by an argv shape the
	// remote shell would never see.
	if name == "ssh" && len(args) > 2 && strings.HasPrefix(args[2], "sqlite3 ") {
		return "ok\n", nil
	}
	return "", nil
}

func (f *fakeReplicationRunner) ran(name string, argSubstring string) bool {
	for _, command := range f.commands {
		if command.name == name && strings.Contains(strings.Join(command.args, " "), argSubstring) {
			return true
		}
	}
	return false
}

type fakeTrawler struct {
	identity string
}

func (t fakeTrawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(t.identity),
		RegisteredTrawlerCommandName: t.identity,
	}
}

func (fakeTrawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error { return nil }

func (fakeTrawler) Status(context.Context, *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	return &status.TrawlerStatusResponse{}, nil
}

func (fakeTrawler) TrawlerCommands() []trawlkit.TrawlerCommand { return nil }

func installedFakeTrawler(identity string) InstalledTrawler {
	return InstalledTrawler{
		RegisteredTrawlerManifest: &federation.RegisteredTrawlerManifest{
			RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(identity),
			RegisteredTrawlerCommandName: identity,
			RegisteredTrawlerDisplayName: identity,
		},
		Trawler: fakeTrawler{identity: identity},
	}
}

// replicatorOverStateRoot builds a replicator whose archives really exist on
// disk, because the command skips trawlers that have never been updated.
func replicatorOverStateRoot(t *testing.T, runner *fakeReplicationRunner, identities ...string) (archiveReplicator, []InstalledTrawler) {
	t.Helper()
	stateRoot := t.TempDir()
	trawlers := make([]InstalledTrawler, 0, len(identities))
	for _, identity := range identities {
		archive := filepath.Join(stateRoot, identity, identity+".db")
		if err := os.MkdirAll(filepath.Dir(archive), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		trawlers = append(trawlers, installedFakeTrawler(identity))
	}
	replicator := archiveReplicator{
		commands: runner,
		locate: trawlkit.NewTrawlerExecutor(trawlkit.TrawlerExecutorOptions{StateRoot: stateRoot}).
			ResolveTrawlerArchiveLocation,
		stderr: io.Discard,
	}
	return replicator, trawlers
}

// A replication that dies halfway leaves the replica holding a mix of two
// archives, so a missing dependency has to stop the run before anything moves.
func TestPreflightRefusesBeforeCopyingWhenADependencyIsMissing(t *testing.T) {
	runner := &fakeReplicationRunner{missingLocal: map[string]bool{"sqlite3_rsync": true}}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp")
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, _, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := replicator.preflight(context.Background(), destination, archives); err == nil {
		t.Fatal("preflight accepted a missing local sqlite3_rsync")
	}
	if runner.ran("sqlite3_rsync", "") {
		t.Fatal("preflight copied an archive before failing")
	}
}

func TestPreflightRefusesWhenTheReplicaHostLacksSQLite(t *testing.T) {
	runner := &fakeReplicationRunner{
		failures: map[string]error{
			"ssh -- host command -v sqlite3": errors.New("command not found"),
		},
	}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp")
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, _, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	err = replicator.preflight(context.Background(), destination, archives)
	if err == nil {
		t.Fatal("preflight accepted a replica host without sqlite3")
	}
	var typed replicationError
	if !errors.As(err, &typed) || typed.code != "remote_dependency_missing" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplicateCopiesValidatesAndProtectsEachArchive(t *testing.T) {
	runner := &fakeReplicationRunner{}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp", "imessage")
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	replicated, err := replicator.replicate(context.Background(), destination, archives, remoteDirs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(replicated, ",") != "whatsapp,imessage" {
		t.Fatalf("unexpected replicated trawlers: %v", replicated)
	}
	if !runner.ran("sqlite3_rsync", "host:/srv/replica/whatsapp/whatsapp.db") {
		t.Fatal("the whatsapp archive was not copied to its place under the replica state root")
	}
	// The replica holds the same personal data as the Mac. It must not be
	// left group- or world-readable on a shared host.
	if !runner.ran("ssh", "chmod 700") {
		t.Fatal("the replica state root was not protected")
	}
	if !runner.ran("ssh", "chmod 600 -- /srv/replica/whatsapp/whatsapp.db") {
		t.Fatal("the whatsapp replica was not protected")
	}
	if !runner.ran("ssh", "PRAGMA quick_check;") {
		t.Fatal("the replica was not validated")
	}
	assertValidationIsOneRemoteArgument(t, runner)
}

// assertValidationIsOneRemoteArgument pins the boundary ssh does not keep. ssh
// joins its arguments with spaces and the remote shell re-splits them, so a
// statement passed as its own argument arrives at sqlite3 as two words:
// "PRAGMA" runs alone, which is incomplete SQL, and the replica reports a
// connection failure for an archive that is intact. Checking the joined
// command is not enough — it looks identical either way, which is why this
// went unnoticed. The statement has to survive as a single argument.
func assertValidationIsOneRemoteArgument(t *testing.T, runner *fakeReplicationRunner) {
	t.Helper()
	for _, command := range runner.commands {
		if command.name != "ssh" {
			continue
		}
		for _, arg := range command.args {
			if !strings.Contains(arg, "quick_check") {
				continue
			}
			if !strings.Contains(arg, "'PRAGMA quick_check;'") {
				t.Fatalf("the validation statement reaches the replica shell split apart: %q", arg)
			}
			return
		}
	}
	t.Fatal("no validation command was run")
}

// A replica that arrived corrupt reads as a successful copy, which is worse
// than no replica at all.
func TestReplicateFailsWhenTheReplicaDoesNotPassIntegrityValidation(t *testing.T) {
	runner := &fakeReplicationRunner{
		responses: map[string]string{
			"ssh -- host sqlite3 -readonly /srv/replica/whatsapp/whatsapp.db 'PRAGMA quick_check;'": "database disk image is malformed",
		},
	}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp")
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	_, err = replicator.replicate(context.Background(), destination, archives, remoteDirs)
	if err == nil {
		t.Fatal("replicate reported success for a corrupt replica")
	}
	var typed replicationError
	if !errors.As(err, &typed) || typed.code != "replica_invalid" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A trawler that has never been updated has no archive. That is not a failure.
func TestReplicateSkipsTrawlersWithNoArchive(t *testing.T) {
	runner := &fakeReplicationRunner{}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp")
	trawlers = append(trawlers, installedFakeTrawler("telegram"))
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	replicated, err := replicator.replicate(context.Background(), destination, archives, remoteDirs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(replicated, ",") != "whatsapp" {
		t.Fatalf("unexpected replicated trawlers: %v", replicated)
	}
}

// Replication reads archives an update would be rewriting. The two must never
// overlap.
func TestReplicationLockIsExclusive(t *testing.T) {
	stateRoot := t.TempDir()
	first, err := acquireReplicationLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	_, err = acquireReplicationLock(stateRoot)
	if err == nil {
		t.Fatal("a second replication acquired the lock while the first held it")
	}
	var alreadyRunning replicationAlreadyRunningError
	if !errors.As(err, &alreadyRunning) {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireReplicationLock(stateRoot)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	_ = second.Close()
}

// Bounded output keeps a failing external tool from pasting an archive's worth
// of private filenames into an error message.
func TestBoundedBufferStopsAtItsLimit(t *testing.T) {
	var buffer boundedBuffer
	written, err := buffer.Write(make([]byte, maxReplicationCommandOutputBytes*2))
	if err != nil {
		t.Fatal(err)
	}
	if written != maxReplicationCommandOutputBytes*2 {
		t.Fatalf("Write reported %d bytes, which would look like a short write to a caller", written)
	}
	if buffer.Len() != maxReplicationCommandOutputBytes {
		t.Fatalf("buffer kept %d bytes, want %d", buffer.Len(), maxReplicationCommandOutputBytes)
	}
}

// An archive records attachment paths relative to its own directory, so a
// database that arrived before its files would point at names that are not
// there yet. Notes keeps every image and document in a note this way.
func TestAttachmentsAreReplicatedBeforeTheDatabaseThatNamesThem(t *testing.T) {
	runner := &fakeReplicationRunner{}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "notes")
	attachments := filepath.Join(filepath.Dir(trawlerArchivePathForTest(t, replicator, trawlers[0])), "attachments")
	if err := os.MkdirAll(filepath.Join(attachments, "abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachments, "abc", "scan.pdf"), []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, remoteDirs, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.replicate(context.Background(), destination, archives, remoteDirs); err != nil {
		t.Fatal(err)
	}

	rsyncAt, databaseAt := -1, -1
	for index, command := range runner.commands {
		if command.name == "rsync" && rsyncAt < 0 {
			rsyncAt = index
		}
		if command.name == "sqlite3_rsync" && databaseAt < 0 {
			databaseAt = index
		}
	}
	if rsyncAt < 0 {
		t.Fatal("the attachments directory was never replicated")
	}
	if databaseAt < 0 {
		t.Fatal("the archive was never replicated")
	}
	if rsyncAt > databaseAt {
		t.Fatal("the database was replicated before the attachments it names")
	}
	if !runner.ran("rsync", "host:/srv/replica/notes/attachments/") {
		t.Fatalf("attachments went to the wrong place: %+v", runner.commands)
	}
}

// An archive with no attachments directory must not require rsync at all.
func TestReplicationDoesNotRequireRsyncWithoutAttachments(t *testing.T) {
	runner := &fakeReplicationRunner{missingLocal: map[string]bool{"rsync": true}}
	replicator, trawlers := replicatorOverStateRoot(t, runner, "whatsapp")
	destination, err := parseReplicationDestination("host:/srv/replica")
	if err != nil {
		t.Fatal(err)
	}
	archives, _, err := replicator.plan(destination, trawlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := replicator.preflight(context.Background(), destination, archives); err != nil {
		t.Fatalf("rsync was required for an archive with no attachments: %v", err)
	}
}

func trawlerArchivePathForTest(t *testing.T, replicator archiveReplicator, trawler InstalledTrawler) string {
	t.Helper()
	located, err := replicator.locate(trawler.Trawler)
	if err != nil {
		t.Fatal(err)
	}
	return located.TrawlerArchivePath
}

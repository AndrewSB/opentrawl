package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func TestParseReplicationDestinationAcceptsNarrowSSHPathAndRejectsShellInput(t *testing.T) {
	valid, err := parseReplicationDestination("backup@archive.example:/srv/opentrawl")
	if err != nil || valid.host != "backup@archive.example" || valid.root != "/srv/opentrawl" {
		t.Fatalf("valid destination = %#v err=%v", valid, err)
	}
	for _, value := range []string{
		"archive.example",
		"-oProxyCommand=bad:/srv/opentrawl",
		"archive.example:/",
		"archive.example:relative",
		"archive.example:/srv/../root",
		"archive.example:/srv/root;touch-bad",
		"user name@archive.example:/srv/root",
		"user@@archive.example:/srv/root",
	} {
		if _, err := parseReplicationDestination(value); err == nil {
			t.Errorf("accepted unsafe destination %q", value)
		}
	}
}

func TestReplicateCopiesNotesAttachmentsBeforeDatabaseAndValidatesEveryReplica(t *testing.T) {
	root := t.TempDir()
	writeReplicationArchive(t, root, "imessage")
	writeReplicationArchive(t, root, "notes")
	attachments := filepath.Join(root, "notes", "attachments")
	if err := os.MkdirAll(attachments, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachments, "synthetic.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &recordingReplicationRunner{}
	var stdout, stderr bytes.Buffer
	runtime := &Runtime{
		ctx:               context.Background(),
		stdout:            &stdout,
		stderr:            &stderr,
		root:              &CLI{},
		stateRoot:         root,
		replicationRunner: runner,
	}
	command := ReplicateCmd{
		Destination: "backup@archive.example:/srv/opentrawl",
		Sources:     []string{"imessage", "notes"},
	}
	if err := command.Run(runtime); err != nil {
		t.Fatal(err)
	}

	rsyncIndex := runner.commandIndex("rsync", "-a")
	imessageIndex := runner.commandIndex("sqlite3_rsync", filepath.Join(root, "imessage", "imessage.db"))
	notesIndex := runner.commandIndex("sqlite3_rsync", filepath.Join(root, "notes", "notes.db"))
	if rsyncIndex < 0 || imessageIndex < 0 || notesIndex < 0 || rsyncIndex > imessageIndex || rsyncIndex > notesIndex {
		t.Fatalf("command order = %#v", runner.commands)
	}
	if got := runner.countCommand("sqlite3_rsync"); got != 2 {
		t.Fatalf("sqlite3_rsync calls = %d, want 2", got)
	}
	if got := runner.countArg("'PRAGMA quick_check;'"); got != 2 {
		t.Fatalf("quick-check calls = %d, want 2; commands=%#v", got, runner.commands)
	}
	if !strings.Contains(stdout.String(), "Replicated imessage, notes to backup@archive.example:/srv/opentrawl.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Messages replicating") || !strings.Contains(stderr.String(), "Notes replicating") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReplicateAndSyncUseOneMutuallyExclusiveLock(t *testing.T) {
	root := t.TempDir()
	writeReplicationArchive(t, root, "imessage")

	syncLock, err := acquireSyncBatchLock(root)
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingReplicationRunner{}
	runtime := replicationTestRuntime(root, runner)
	err = (&ReplicateCmd{Destination: "archive.example:/srv/opentrawl", Sources: []string{"imessage"}}).Run(runtime)
	var replicateBusy replicateAlreadyRunningError
	if !errors.As(err, &replicateBusy) || replicateBusy.active != "sync" {
		t.Fatalf("replicate while sync active err=%#v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("busy replication ran remote commands: %#v", runner.commands)
	}
	if err := syncLock.Close(); err != nil {
		t.Fatal(err)
	}

	replicationLock, err := acquireReplicationLock(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireSyncBatchLock(root)
	var syncBusy syncAlreadyRunningError
	if !errors.As(err, &syncBusy) || syncBusy.active != "replicate" {
		t.Fatalf("sync while replicate active err=%#v", err)
	}
	if err := replicationLock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplicationFailureReleasesExclusiveLock(t *testing.T) {
	root := t.TempDir()
	writeReplicationArchive(t, root, "imessage")
	runner := &recordingReplicationRunner{failName: "sqlite3_rsync"}
	err := (&ReplicateCmd{Destination: "archive.example:/srv/opentrawl", Sources: []string{"imessage"}}).Run(replicationTestRuntime(root, runner))
	if err == nil {
		t.Fatal("replication succeeded, want injected transfer failure")
	}
	lock, lockErr := acquireSyncBatchLock(root)
	if lockErr != nil {
		t.Fatalf("sync lock remained held after failure: %v", lockErr)
	}
	_ = lock.Close()
}

func TestReplicationRequiresAnExistingArchiveAndDoesNotCreateIt(t *testing.T) {
	root := t.TempDir()
	runner := &recordingReplicationRunner{}
	err := (&ReplicateCmd{Destination: "archive.example:/srv/opentrawl", Sources: []string{"imessage"}}).Run(replicationTestRuntime(root, runner))
	var typed replicationError
	if !errors.As(err, &typed) || typed.code != "archive_missing" {
		t.Fatalf("missing archive err=%#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "imessage", "imessage.db")); !os.IsNotExist(statErr) {
		t.Fatalf("replication created missing archive: %v", statErr)
	}
}

func TestReplicationUsesResolvedArchivePathAndPreservesStateRootLayout(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "custom", "messages.sqlite")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, []byte("synthetic sqlite placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingReplicationRunner{}
	replicator := archiveReplicator{
		commands: runner,
		paths: func(trawlkit.Crawler) (trawlkit.SourcePaths, error) {
			return trawlkit.SourcePaths{StateRoot: root, Paths: trawlkit.Paths{Archive: archivePath}}, nil
		},
		stderr: &bytes.Buffer{},
	}
	sources := []Source{{ID: "imessage", DisplayName: "Messages"}}
	if err := replicator.replicate(context.Background(), replicationDestination{host: "archive.example", root: "/srv/opentrawl"}, sources); err != nil {
		t.Fatal(err)
	}
	command := runner.commands[runner.commandIndex("sqlite3_rsync", archivePath)]
	if got, want := command.args[1], "archive.example:/srv/opentrawl/custom/messages.sqlite"; got != want {
		t.Fatalf("remote archive = %q, want %q", got, want)
	}
}

func TestReplicationRejectsArchiveOutsideStateRoot(t *testing.T) {
	root := t.TempDir()
	replicator := archiveReplicator{
		commands: &recordingReplicationRunner{},
		paths: func(trawlkit.Crawler) (trawlkit.SourcePaths, error) {
			return trawlkit.SourcePaths{StateRoot: root, Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "messages.db")}}, nil
		},
		stderr: &bytes.Buffer{},
	}
	err := replicator.replicate(context.Background(), replicationDestination{host: "archive.example", root: "/srv/opentrawl"}, []Source{{ID: "imessage", DisplayName: "Messages"}})
	var typed replicationError
	if !errors.As(err, &typed) || typed.code != "archive_not_portable" {
		t.Fatalf("outside-root archive err=%#v", err)
	}
}

func writeReplicationArchive(t *testing.T, root, source string) {
	t.Helper()
	directory := filepath.Join(root, source)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, source+".db"), []byte("synthetic sqlite placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func replicationTestRuntime(root string, runner replicationCommandRunner) *Runtime {
	return &Runtime{
		ctx:               context.Background(),
		stdout:            &bytes.Buffer{},
		stderr:            &bytes.Buffer{},
		root:              &CLI{},
		stateRoot:         root,
		replicationRunner: runner,
	}
}

type recordedCommand struct {
	name string
	args []string
}

type recordingReplicationRunner struct {
	commands []recordedCommand
	failName string
}

func (r *recordingReplicationRunner) LookPath(name string) (string, error) {
	if name == r.failName {
		return "", errors.New("injected missing executable")
	}
	return "/synthetic/bin/" + name, nil
}

func (r *recordingReplicationRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if name == r.failName {
		return "", errors.New("injected command failure")
	}
	if name == "ssh" && len(args) > 0 && args[len(args)-1] == "'PRAGMA quick_check;'" {
		return "ok\n", nil
	}
	return "/synthetic/bin/tool\n", nil
}

func (r *recordingReplicationRunner) commandIndex(name string, firstArg string) int {
	for index, command := range r.commands {
		if command.name == name && (firstArg == "" || len(command.args) > 0 && command.args[0] == firstArg) {
			return index
		}
	}
	return -1
}

func (r *recordingReplicationRunner) countCommand(name string) int {
	count := 0
	for _, command := range r.commands {
		if command.name == name {
			count++
		}
	}
	return count
}

func (r *recordingReplicationRunner) countArg(value string) int {
	count := 0
	for _, command := range r.commands {
		for _, arg := range command.args {
			if reflect.DeepEqual(arg, value) {
				count++
			}
		}
	}
	return count
}

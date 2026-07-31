package whatsapp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"

	_ "github.com/mattn/go-sqlite3"

	"bytes"
)

// TestSearchAnswersOneHitPerRefWhenTheSourceStoredAMessageTwice imports a
// source database in the state a history re-sync leaves behind: one message
// stored under two of WhatsApp's own row keys, both carrying the same stanza id.
// A real archive reached that state — 4,008 of 19,153 rows — and it made every
// federated search of the source fail, because the projection rejects a page
// that names one ref twice and the source had no way to tell the copies apart.
//
// The assertions are the contract's, not this package's: a ref names one
// record, a page carries it once, and the copy search returns is the copy open
// returns.
func TestSearchAnswersOneHitPerRefWhenTheSourceStoredAMessageTwice(t *testing.T) {
	ctx := context.Background()
	sourceRoot := t.TempDir()
	createDesktopFixture(t, sourceRoot)
	appendResyncedCopy(t, sourceRoot)

	stateRoot := t.TempDir()
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "whatsapp", "whatsapp.db"),
		Config:  filepath.Join(stateRoot, "whatsapp", "config.toml"),
		Logs:    filepath.Join(stateRoot, "whatsapp", "logs"),
	}
	crawler := New()
	crawler.cfg.Source = sourceRoot

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   output.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	if _, err := crawler.Sync(ctx, syncReq); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	// Syncing migrates the archive, so the index the contact join needs is
	// created by the sync that writes the archive rather than by hand. Without
	// it every read of this source rebuilds the person corpus through a join no
	// index can serve, which is minutes of CPU on a real archive.
	readIndex := openReadStore(t, ctx, paths.Archive)
	var indexName string
	err = readIndex.DB().QueryRowContext(ctx,
		`select name from sqlite_master where type = 'index' and name = 'idx_contacts_lid'`).Scan(&indexName)
	_ = readIndex.Close()
	if err != nil {
		t.Fatalf("sync did not create the contact lookup index: %v", err)
	}

	// The archive keeps both rows. Deciding which of the source's rows are real
	// is not the mirror's decision to make.
	readStore := openReadStore(t, ctx, paths.Archive)
	var rows int
	if err := readStore.DB().QueryRowContext(ctx, `select count(*) from messages where msg_id = 'dm-in'`).Scan(&rows); err != nil {
		_ = readStore.Close()
		t.Fatal(err)
	}
	if rows != 2 {
		_ = readStore.Close()
		t.Fatalf("the mirror dropped a source row: %d rows for one message id", rows)
	}

	search, err := crawler.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Text: "hello", Limit: 20})
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}
	for _, hit := range search.Results {
		identity := hit.Ref + "\x00" + hit.AnchorID
		if _, exists := seen[identity]; exists {
			t.Fatalf("ref %q and anchor %q were returned twice; a federated search of this source fails outright", hit.Ref, hit.AnchorID)
		}
		seen[identity] = struct{}{}
	}
	if len(search.Results) != 1 {
		t.Fatalf("expected one hit for one message, got %d: %#v", len(search.Results), search.Results)
	}
	if search.TotalMatches != 1 {
		t.Fatalf("total counts rows rather than refs: %d", search.TotalMatches)
	}

	// And the copy search returned is the copy open resolves the ref to: the
	// newest row, by the record's own timestamp and then by source row key.
	readStore = openReadStore(t, ctx, paths.Archive)
	var opened int64
	err = readStore.DB().QueryRowContext(ctx,
		`select ts from messages where msg_id = 'dm-in' order by ts desc, source_pk desc limit 1`).Scan(&opened)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.Results[0].Time.Unix() != opened {
		t.Fatalf("search returned the copy timed %d, open resolves the ref to the one timed %d",
			search.Results[0].Time.Unix(), opened)
	}
}

// appendResyncedCopy stores one existing message a second time under a fresh
// row key, which is what WhatsApp Desktop does when it backfills history it
// already holds. The stanza id, the chat, and the text are unchanged, so the
// two rows are one message; the backfilled copy carries the later stored date,
// which is the one a reader should be shown.
func appendResyncedCopy(t *testing.T, dir string) {
	t.Helper()
	chat, err := sql.Open("sqlite3", filepath.Join(dir, "ChatStorage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = chat.Close() }()
	mustExec(t, chat, `
insert into ZWAMESSAGE values (4001, 1, null, null, 'dm-in', 0, 700000005, 'hello from bob', 0, 0, '15550111@s.whatsapp.net', '', 'Bob Example');
`)
}

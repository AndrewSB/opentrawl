package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// storeWithResyncedCopy returns a store holding one message stored twice, which
// is the state a WhatsApp history re-sync leaves behind: the source backfills
// history it already holds and assigns the copy a fresh row key, so two rows
// carry one stanza id. A real archive reached that state for 4,008 of its
// 19,153 rows.
//
// The backfilled copy carries the later time, so it is the one `open` resolves
// the ref to.
func storeWithResyncedCopy(t *testing.T) (context.Context, *Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	contacts := []Contact{{JID: "alice@s.whatsapp.net", FullName: "Alice", UpdatedAt: now}}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now, MessageCount: 2}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "dm-in", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now.Add(-time.Hour), Text: "hello launch", MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "dm-in", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "hello launch", MessageType: "text"},
	}
	if err := st.ReplaceAll(ctx, stats, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return ctx, st, now
}

// The mirror keeps both of the source's rows. Deciding which of them are real is
// not the mirror's decision to make.
func TestReplaceAllKeepsEveryRowTheSourceStoredForOneRef(t *testing.T) {
	ctx, st, _ := storeWithResyncedCopy(t)
	var rows int
	if err := st.DB().QueryRowContext(ctx, `select count(*) from messages where msg_id = 'dm-in'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("the mirror dropped a source row: %d rows for one message id", rows)
	}
}

// A page that named one ref twice would offer one record twice under one name,
// which the federation layer rejects outright.
func TestSearchReturnsOneHitPerRefWhenTheSourceStoredAMessageTwice(t *testing.T) {
	ctx, st, _ := storeWithResyncedCopy(t)
	hits, err := st.Search(ctx, MessageFilter{Query: "hello", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one hit for one message, got %d: %+v", len(hits), hits)
	}

	total, err := st.SearchCount(ctx, MessageFilter{Query: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total counts rows rather than refs: %d", total)
	}
}

// A hit and the record it opens are never different copies.
func TestSearchReturnsTheCopyOpenResolvesTheRefTo(t *testing.T) {
	ctx, st, _ := storeWithResyncedCopy(t)
	hits, err := st.Search(ctx, MessageFilter{Query: "hello", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	opened, err := st.MessageByID(ctx, "dm-in")
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].SourcePK != opened.SourcePK {
		t.Fatalf("search returned row %d, open resolves the ref to row %d", hits[0].SourcePK, opened.SourcePK)
	}
	if !hits[0].Timestamp.Equal(opened.Timestamp) {
		t.Fatalf("search returned the copy timed %s, open resolves the ref to the one timed %s",
			hits[0].Timestamp, opened.Timestamp)
	}
}

// Listing is a search with no query, and answers the same ref rule.
func TestListingReturnsOneRowPerRefWhenTheSourceStoredAMessageTwice(t *testing.T) {
	ctx, st, now := storeWithResyncedCopy(t)
	after := now.Add(-48 * time.Hour)
	hits, err := st.Search(ctx, MessageFilter{ChatJID: "chat@g.us", After: &after, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one row for one message, got %d: %+v", len(hits), hits)
	}
}

// Collapsing must not return a page shorter than the limit while the archive
// still holds unseen refs.
func TestSearchFillsThePageWhenDuplicatesAreCollapsed(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now}}
	var messages []Message
	sourcePK := int64(1)
	// Four refs, each stored twice, newest first.
	for ref := range 4 {
		for copyIndex := range 2 {
			messages = append(messages, Message{
				SourcePK:    sourcePK,
				ChatJID:     "chat@g.us",
				ChatName:    "Chat",
				MessageID:   string(rune('a' + ref)),
				SenderJID:   "alice@s.whatsapp.net",
				SenderName:  "Alice",
				Timestamp:   now.Add(-time.Duration(ref)*time.Hour - time.Duration(copyIndex)*time.Minute),
				Text:        "hello launch",
				MessageType: "text",
			})
			sourcePK++
		}
	}
	if err := st.ReplaceAll(ctx, stats, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	hits, err := st.Search(ctx, MessageFilter{Query: "hello", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("collapsing returned a short page: got %d hits, want 3", len(hits))
	}
	seen := map[string]struct{}{}
	for _, hit := range hits {
		if _, duplicate := seen[hit.MessageID]; duplicate {
			t.Fatalf("ref %q was returned twice", hit.MessageID)
		}
		seen[hit.MessageID] = struct{}{}
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A WhatsApp history re-sync backfills messages the source already holds under
// a fresh row key, so two rows carry one stanza id. Status counts what a reader
// can reach, which is one message, and reports the unreachable rows separately
// rather than folding the difference away.
func TestStatusCountsRefsAndReportsTheRowsBeyondThem(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "dm-in", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now.Add(-time.Hour), Text: "hello", MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "dm-in", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "hello", MessageType: "text"},
		{SourcePK: 3, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "pic", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "photo", MessageType: "image", MediaType: "image", MediaPath: "/tmp/a.jpg"},
		{SourcePK: 4, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "pic", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "photo", MessageType: "image", MediaType: "image", MediaPath: "/tmp/a.jpg"},
	}
	if err := st.ReplaceAll(ctx, stats, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 {
		t.Fatalf("messages counts rows rather than refs: got %d, want 2", status.Messages)
	}
	if status.MediaMessages != 1 {
		t.Fatalf("media messages counts rows rather than refs: got %d, want 1", status.MediaMessages)
	}
	if status.DuplicateMessageRows != 2 {
		t.Fatalf("the rows beyond the reachable messages went unreported: got %d, want 2", status.DuplicateMessageRows)
	}

	// The mirror still holds every row the source stored.
	var rows int
	if err := st.DB().QueryRowContext(ctx, `select count(*) from messages`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 4 {
		t.Fatalf("the mirror dropped a source row: %d rows", rows)
	}
}

// An archive with no duplicated rows reports none, and its message count is
// unchanged by counting refs.
func TestStatusReportsNoDuplicateRowsForACleanArchive(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "a", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "hello", MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "b", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now, Text: "there", MessageType: "text"},
	}
	if err := st.ReplaceAll(ctx, stats, nil, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 {
		t.Fatalf("unexpected message count: got %d, want 2", status.Messages)
	}
	if status.DuplicateMessageRows != 0 {
		t.Fatalf("a clean archive reported %d duplicate rows", status.DuplicateMessageRows)
	}
}

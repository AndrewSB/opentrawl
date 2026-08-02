package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// storeWithLIDSender returns a store whose only message is sent by a contact
// known to the archive by its LID rather than its phone JID, so reading the
// message has to resolve the sender through the LID arm of the contact join.
func storeWithLIDSender(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	contacts := []Contact{{JID: "alice@s.whatsapp.net", LID: "111222333", FullName: "Alice Example", UpdatedAt: now}}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now, MessageCount: 1}}
	groups := []Group{{JID: "chat@g.us", Name: "Chat", CreatedAt: now.Add(-time.Hour)}}
	participants := []GroupParticipant{{GroupJID: "chat@g.us", UserJID: "111222333@lid", ContactName: "Alice Example", IsActive: true}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "m1", SenderJID: "111222333@lid", SenderName: "alice", Timestamp: now, Text: "hello launch", MessageType: "text"},
	}
	if err := st.ReplaceAll(ctx, stats, contacts, chats, groups, participants, messages); err != nil {
		t.Fatal(err)
	}
	return ctx, st
}

// The contact join resolves a LID-suffixed sender against an indexed column, so
// the index has to exist for the archive the store opens rather than be created
// by hand.
func TestOpenCreatesTheContactLookupIndex(t *testing.T) {
	ctx, st := storeWithLIDSender(t)
	var name string
	err := st.DB().QueryRowContext(ctx,
		`select name from sqlite_master where type = 'index' and name = 'idx_contacts_lid'`).Scan(&name)
	if err != nil {
		t.Fatalf("the archive has no contact lookup index, so every read scans contacts: %v", err)
	}
}

// Rewriting the join to compare an indexed column must not change which
// contacts it matches: a sender JID of "<lid>@lid" still resolves to the
// contact holding that LID.
func TestSenderResolvesThroughTheLIDArmOfTheContactJoin(t *testing.T) {
	ctx, st := storeWithLIDSender(t)
	resolution, err := st.ResolveWho(ctx, "Alice Example")
	if err != nil {
		t.Fatal(err)
	}
	var identifiers []string
	for _, candidate := range resolution.Candidates {
		identifiers = append(identifiers, candidate.Identifiers...)
	}
	joined := strings.Join(identifiers, " ")
	if !strings.Contains(joined, "alice@s.whatsapp.net") {
		t.Fatalf("the LID sender did not resolve to its contact: %+v", resolution)
	}
}

// The join has to stay index-driven. A plan that scans contacts for each
// candidate row is the regression this pins: on a real archive it was minutes
// of CPU on every read.
func TestContactJoinUsesTheLIDIndex(t *testing.T) {
	ctx, st := storeWithLIDSender(t)
	predicate := contactJIDPredicate("c", "'111222333@lid'")
	rows, err := st.DB().QueryContext(ctx,
		`explain query plan select c.jid from contacts c where `+predicate)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.String(), "SCAN c") {
		t.Fatalf("the contact join scans contacts instead of using an index:\n%s", plan.String())
	}
	if !strings.Contains(plan.String(), "idx_contacts_lid") {
		t.Fatalf("the contact join does not use the LID index:\n%s", plan.String())
	}
}

// The sender display-name map reads names, not counts. Skipping the per-message
// statistics must not change the name it returns for a sender.
func TestDisplayNameMapAgreesWithTheStatisticsPath(t *testing.T) {
	ctx, st := storeWithLIDSender(t)
	withStats, err := st.whoCandidateRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	namesOnly, err := st.whoCandidateNameRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	statsNames := canonicalWhatsAppMessageDisplayNamesFromWhoCandidateRecords(withStats)
	skipNames := canonicalWhatsAppMessageDisplayNamesFromWhoCandidateRecords(namesOnly)
	for key, want := range statsNames.canonicalSenderNamesByIdentityKey {
		if got := skipNames.canonicalSenderNamesByIdentityKey[key]; got != want {
			t.Fatalf("skipping statistics changed the name for %q: got %q, want %q", key, got, want)
		}
	}
}

package imessage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	imessages "github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenMessage")
}

func TestMessagesHumanOutputUsesShortRefsForRowsAndContinuation(t *testing.T) {
	var stdout bytes.Buffer
	err := printMessagesText(&stdout, messageListOutput{
		listHeader: listHeader{Returned: 1, Total: 2, Limit: 1, Complete: false},
		ChatID:     "42",
		Order:      "newest-first",
		chatHandle: "chatref",
		Items: []archive.MessageRow{{
			Ref:      "imessage:msg/7",
			ShortRef: "msgref",
			Time:     "2026-07-16T10:00:00Z",
			Text:     "Synthetic message",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(chat chatref)", "--chat chatref", "msgref", "Synthetic message"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("human output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestMessagesListsArchiveWideBoundedWindowOnceInStableOrder(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "imessage.db")
	st, err := archive.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	window := first.Add(time.Hour)
	data := imessages.ArchiveData{
		SourcePath:       "synthetic-chat.db",
		SourceModifiedAt: first,
		ExtractedAt:      first,
		Handles: []imessages.Handle{
			{SourceRowID: 1, ID: "+15550001001", DisplayName: "Avery Example"},
			{SourceRowID: 2, ID: "+15550001002", DisplayName: "Morgan Example"},
		},
		Chats: []imessages.Chat{
			{SourceRowID: 1, GUID: "chat-one", DisplayName: "Project Lantern"},
			{SourceRowID: 2, GUID: "chat-two", DisplayName: "Weekend Plans"},
		},
		Participants: []imessages.Participant{
			{ChatRowID: 1, HandleRowID: 1},
			{ChatRowID: 2, HandleRowID: 2},
		},
		ChatMessages: []imessages.ChatMessage{
			{ChatRowID: 1, MessageRowID: 1},
			{ChatRowID: 1, MessageRowID: 2},
			{ChatRowID: 2, MessageRowID: 2},
			{ChatRowID: 2, MessageRowID: 3},
		},
		Messages: []imessages.Message{
			{SourceRowID: 1, GUID: "message-one", HandleRowID: 1, Date: archive.AppleDateFromTime(first), Text: "Outside window"},
			{SourceRowID: 2, GUID: "message-two", HandleRowID: 1, Date: archive.AppleDateFromTime(window), Text: "First bounded message"},
			{SourceRowID: 3, GUID: "message-three", HandleRowID: 2, Date: archive.AppleDateFromTime(window), Text: "Second bounded message"},
		},
	}
	if err := st.ReplaceAll(ctx, data, nil, nil, first); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, archivePath)
	defer func() { _ = readStore.Close() }()
	source := New()
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	source.bindMessagesFlags(fs)
	bound := window.Format(time.RFC3339)
	if err := fs.Parse([]string{"--after", bound, "--before", bound, "--all", "--asc"}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	req := &trawlkit.Request{
		Store: readStore, Paths: trawlkit.Paths{Archive: archivePath},
		Format: ckoutput.JSON, Out: &stdout,
	}
	if err := source.runMessages(ctx, req); err != nil {
		t.Fatal(err)
	}
	var got trawlkit.MessageList
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || got.Truncated || len(got.Messages) != 2 {
		t.Fatalf("bounded messages = %#v", got)
	}
	if got.Messages[0].Ref != "imessage:msg/2" || got.Messages[1].Ref != "imessage:msg/3" {
		t.Fatalf("bounded message order = %#v", got.Messages)
	}
	for _, message := range got.Messages {
		if message.Where == "" || message.Text == "" {
			t.Fatalf("message lost source projection: %#v", message)
		}
	}
}

func TestMessagesRejectsAllWithLimitBeforeOpeningArchive(t *testing.T) {
	source := New()
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	source.bindMessagesFlags(fs)
	if err := fs.Parse([]string{"--all", "--limit", "10"}); err != nil {
		t.Fatal(err)
	}
	err := source.runMessages(context.Background(), &trawlkit.Request{})
	if err == nil || !strings.Contains(err.Error(), "--all and --limit") {
		t.Fatalf("error = %v", err)
	}
}

func assertOpenRecordLoaderCall(t *testing.T, path, loader string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "OpenRecord" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == loader {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("OpenRecord %s calls = %d, want 1", loader, calls)
	}
}

func TestStatusUsesOnlyArchiveState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	request := &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "messages.db")}}
	status, err := New().Status(context.Background(), request)
	if err != nil || status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("archive-only status = %#v, %v", status, err)
	}
}

func TestCrawlerSyncSearchOpenAndContacts(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	report, err := source.Sync(ctx, syncReq)
	if err == nil {
		records, recordsErr := source.ShortRefRecords(ctx, syncReq)
		if recordsErr != nil {
			err = recordsErr
		} else if _, assignErr := syncReq.AssignShortRefs(ctx, records); assignErr != nil {
			err = assignErr
		}
	}
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 5 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 5 added, 0 updated, 0 removed", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	searchReq := readRequest(readStore, paths)
	search, err := source.Search(ctx, searchReq, trawlkit.Query{Text: "launch", Limit: 20})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 2 || len(search.Results) != 2 {
		t.Fatalf("search = %#v, want two results", search)
	}
	hit := search.Results[0]
	if !strings.HasPrefix(hit.Ref, archive.MessageRefPrefix) || hit.ShortRef == "" {
		t.Fatalf("search hit refs = %#v", hit)
	}
	if hit.AnchorID != trawlkit.MatchAnchorID || hit.Summary.Title != "Most Recent Name" || hit.Summary.Subtitle == "" {
		t.Fatalf("search hit = %#v", hit)
	}
	if len(hit.Evidence) != 1 || hit.Evidence[0].Text == nil || len(hit.Evidence[0].Text.Runs) != 1 || !hit.Evidence[0].Text.Runs[0].Matched || !strings.Contains(hit.Evidence[0].Text.Runs[0].Text, "launch") {
		t.Fatalf("search evidence = %#v", hit.Evidence)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	fullRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	shortRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, hit.ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != hit.Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.imessage.open.v1.IMessageRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	load := func(ref string) archive.MessageContext {
		readStore = openReadStore(t, ctx, paths.Archive)
		value, loadErr := source.loadOpenMessage(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return value
	}
	writeRuntimeOpenEvidence(t, "full", hit.Ref, load(hit.Ref), fullRecord)
	writeRuntimeOpenEvidence(t, "short", hit.ShortRef, load(hit.ShortRef), shortRecord)
	assertOpenRecordError := func(ref, want string) {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		var typed commandError
		if !errors.As(err, &typed) || typed.name != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	assertOpenRecordError("zzzzz", "unknown_short_ref")
	assertOpenRecordError("photos:asset/example", "foreign_ref")
	assertOpenRecordError("imessage:msg/not-a-number", "invalid_ref")
	assertOpenRecordError("imessage:msg/999999999", "not_found")
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", hit.Ref, hit.Ref, "zzzzz", "imessage:msg/999999999", "imessage:msg/999999999"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: writeStore, Paths: paths}, "zzzzz")
	_ = writeStore.Close()
	var ambiguous commandError
	if !errors.As(err, &ambiguous) || ambiguous.name != "ambiguous_short_ref" {
		t.Fatalf("ambiguous short ref error = %#v", err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: paths.Archive + ".missing"}}, hit.Ref)
	var archiveFailure commandError
	if !errors.As(err, &archiveFailure) || archiveFailure.name != "archive" {
		t.Fatalf("missing archive error = %#v", err)
	}

	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `update messages set date = ? where source_rowid = ?`, "not a timestamp", 2); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: writeStore, Paths: paths}, hit.Ref)
	_ = writeStore.Close()
	if err == nil {
		t.Fatal("malformed stored timestamp opened")
	}
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `update messages set date = ? where source_rowid = ?`, 200, 2); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	_ = writeStore.Close()

	readStore = openReadStore(t, ctx, paths.Archive)
	contacts, err := source.PeopleSnapshot(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 3 || contacts.Contacts[0].SourceID == "" || contacts.Contacts[0].DisplayName != "Fixture Person" || contacts.Contacts[0].PhoneNumbers[0] != "+15550103" || contacts.Contacts[2].EmailAddresses[0] != "person@example.test" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

// A message can hold no text and no attachment (a reaction shell, an
// edited-away body). Search evidence must never be an empty run — federation
// rejects one and the whole source's page fails with it — so such a message
// renders as "(no content)".
func TestSearchRendersMessageWithNoTextAndNoAttachment(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesSource(t, sourcePath, []string{
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550100', 'iMessage', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'chat-one', 'Fixture Person', '+15550100', 'iMessage', '', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (1, 'message-one', 1, 100, 'iMessage', 0, 'synthetic hello', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (2, 'message-two', 1, 200, 'iMessage', 0, null, null, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 2)`,
	})

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	_, err = source.Sync(ctx, syncReq)
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	search, err := source.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Limit: 20, After: time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)})
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 2 {
		t.Fatalf("search = %#v, want both messages", search)
	}
	texts := map[string]string{}
	for _, hit := range search.Results {
		if len(hit.Evidence) != 1 || hit.Evidence[0].Text == nil || len(hit.Evidence[0].Text.Runs) != 1 {
			t.Fatalf("search evidence = %#v", hit.Evidence)
		}
		run := hit.Evidence[0].Text.Runs[0]
		if run.Text == "" {
			t.Fatalf("hit %s carries an empty evidence run federation would reject", hit.Ref)
		}
		texts[hit.Ref] = run.Text
	}
	if texts[archive.MessageRef("1")] != "synthetic hello" {
		t.Fatalf("text message evidence = %q", texts[archive.MessageRef("1")])
	}
	if texts[archive.MessageRef("2")] != "(no content)" {
		t.Fatalf("empty message evidence = %q, want (no content)", texts[archive.MessageRef("2")])
	}
}

func TestChatsListsConversationsWithReadState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	if _, err := source.Sync(ctx, syncReq); err != nil {
		t.Fatal(err)
	}
	records, err := source.ShortRefRecords(ctx, syncReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncReq.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	req := readRequest(readStore, paths)

	chats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 4 {
		t.Fatalf("chats = %d, want 4: %#v", len(chats), chats)
	}
	// Every chat reports a real unread count once the archive has ingested
	// read state. The counts prove the semantics: a read received message and
	// an owner-sent message never count, and one unread message shared by two
	// chats counts in both. Expected: chat-one 1, chat-two 0, chat-three 1,
	// chat-four 2, so the sorted multiset is {0, 1, 1, 2}.
	var unreadValues []int64
	var group *trawlkit.Chat
	for i := range chats {
		if chats[i].Unread == nil {
			t.Fatalf("read state was synced; unread must be set, not nil: %#v", chats[i])
		}
		unreadValues = append(unreadValues, *chats[i].Unread)
		if chats[i].Participants == nil {
			t.Fatalf("iMessage counts participants; the count must be set: %#v", chats[i])
		}
		if chats[i].Group {
			group = &chats[i]
		}
	}
	sort.Slice(unreadValues, func(i, j int) bool { return unreadValues[i] < unreadValues[j] })
	if got, want := unreadValues, []int64{0, 1, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unread counts = %v, want %v", got, want)
	}
	// The fixture's room-named chat has three handles, so it is a group; the
	// rest are one-to-one dms.
	if group == nil || *group.Participants < 3 {
		t.Fatalf("expected one group chat with 3+ participants: %#v", group)
	}
	// The chat column is the ref (imessage:chat/<id>); it must survive a round
	// trip into messages --chat and land on the same chat as the raw id.
	if got := archive.ChatRef(group.ID); got != "imessage:chat/"+group.ID {
		t.Fatalf("chat ref = %q", got)
	}
	rawOut := runImessageMessages(t, ctx, source, readStore, paths, group.ID)
	refOut := runImessageMessages(t, ctx, source, readStore, paths, "imessage:chat/"+group.ID)
	if rawOut == "" || rawOut != refOut {
		t.Fatalf("messages --chat ref must resolve identically to the raw id:\nraw=%s\nref=%s", rawOut, refOut)
	}
	var listed trawlkit.MessageList
	if err := json.Unmarshal([]byte(rawOut), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Messages) == 0 || listed.Messages[0].Ref == "" || listed.Messages[0].ShortRef == "" {
		t.Fatalf("messages must expose openable canonical and human refs: %s", rawOut)
	}
	for _, privateField := range []string{"message_id", "guid", "chat_id", "handle_id", "sender_handle", "service"} {
		if strings.Contains(rawOut, `"`+privateField+`"`) {
			t.Fatalf("messages JSON exposed archive field %q: %s", privateField, rawOut)
		}
	}

	// --unread returns only the chats that have unread received messages.
	unreadChats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadChats) != 3 {
		t.Fatalf("--unread chats = %d, want 3: %#v", len(unreadChats), unreadChats)
	}
	for i := range unreadChats {
		if unreadChats[i].Unread == nil || *unreadChats[i].Unread == 0 {
			t.Fatalf("--unread must return only chats with a positive unread count: %#v", unreadChats[i])
		}
	}
}

// A pre-migration archive lacks the messages.is_read column. It must still
// list chats, with Unread nil rather than a fake zero, and refuse --unread
// with ErrChatsNoReadState so a stale archive degrades honestly until re-sync.
func TestChatsDegradesHonestlyWithoutReadStateColumn(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	stateRoot := filepath.Join(home, ".opentrawl")
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, appID, appID+".db"),
		Config:  filepath.Join(stateRoot, appID, "config.toml"),
		Logs:    filepath.Join(stateRoot, appID, "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Sync(ctx, &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate an archive synced before read-state ingestion by removing the
	// column the read path probes for.
	if _, err := writeStore.DB().ExecContext(ctx, `alter table messages drop column is_read`); err != nil {
		t.Fatalf("drop is_read column: %v", err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	req := readRequest(readStore, paths)

	chats, err := source.Chats(ctx, req, trawlkit.ChatQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 4 {
		t.Fatalf("chats = %d, want 4: %#v", len(chats), chats)
	}
	for i := range chats {
		if chats[i].Unread != nil {
			t.Fatalf("a pre-migration archive stores no read state; unread must stay nil: %#v", chats[i])
		}
	}

	if _, err := source.Chats(ctx, req, trawlkit.ChatQuery{Unread: true}); !errors.Is(err, trawlkit.ErrChatsNoReadState) {
		t.Fatalf("--unread on a read-state-less archive must be ErrChatsNoReadState, got %v", err)
	}
}

// A sync replaces every source-derived table in the archive. Short refs are a
// published citation contract, so the index must outlive that replacement: an
// alias issued by an earlier sync keeps naming the same record after the
// source rows are rewritten in a different order, gain new rows and lose old
// ones. The alias of a deleted record survives too, so citing it reports not
// found instead of quietly landing on somebody else's message.
func TestShortRefsSurviveFullArchiveReplacement(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := trawlkit.Paths{
		Archive: filepath.Join(home, ".opentrawl", appID, appID+".db"),
		Config:  filepath.Join(home, ".opentrawl", appID, "config.toml"),
		Logs:    filepath.Join(home, ".opentrawl", appID, "logs"),
	}
	source := New()

	createMessagesSource(t, sourcePath, identityFixtureInserts())
	withWriteRequest(t, ctx, paths, func(req *trawlkit.Request) error {
		_, err := source.Sync(ctx, req)
		return err
	})
	// An alias an earlier archive generation issued to a record the source no
	// longer holds. It occupies the shortest alias of message three, so
	// assignment must extend that one rather than move this one.
	extendedRef := archive.MessageRef("3")
	squattedAlias := shortref.Alias(extendedRef, shortref.MinLength)
	withWriteRequest(t, ctx, paths, func(req *trawlkit.Request) error {
		_, err := req.Store.DB().ExecContext(ctx,
			`insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?)`,
			squattedAlias, retiredIdentityRef, retiredIdentityRef)
		return err
	})
	assignShortRefs(t, ctx, source, paths)

	deletedRef := archive.MessageRef("2")
	issuedRefs := []string{
		archive.MessageRef("1"),
		deletedRef,
		extendedRef,
		archive.ChatRef("1"),
		archive.ChatRef("2"),
	}
	issued := shortRefAliasesFor(t, ctx, paths, issuedRefs)
	for _, ref := range issuedRefs {
		if issued[ref] == "" {
			t.Fatalf("first sync issued no alias for %q: %#v", ref, issued)
		}
	}
	if issued[extendedRef] == squattedAlias || !strings.HasPrefix(issued[extendedRef], squattedAlias) {
		t.Fatalf("collided alias = %q, want an extension of %q", issued[extendedRef], squattedAlias)
	}

	// Second sync: the same records written in a different order, one record
	// deleted and three added.
	createMessagesSource(t, sourcePath, churnedIdentityFixtureInserts())
	withWriteRequest(t, ctx, paths, func(req *trawlkit.Request) error {
		_, err := source.Sync(ctx, req)
		return err
	})
	assignShortRefs(t, ctx, source, paths)

	resynced := shortRefAliasesFor(t, ctx, paths, issuedRefs)
	for _, ref := range issuedRefs {
		if resynced[ref] != issued[ref] {
			t.Fatalf("alias for %q changed across replacement: got %q want %q", ref, resynced[ref], issued[ref])
		}
		assertShortRefResolvesTo(t, ctx, paths, issued[ref], ref)
	}

	// New records get aliases of their own; none of them may take an alias a
	// reader has already been given.
	addedRefs := []string{archive.MessageRef("4"), archive.MessageRef("5"), archive.ChatRef("3")}
	added := shortRefAliasesFor(t, ctx, paths, addedRefs)
	for _, ref := range addedRefs {
		if added[ref] == "" {
			t.Fatalf("second sync issued no alias for new ref %q: %#v", ref, added)
		}
		assertShortRefResolvesTo(t, ctx, paths, added[ref], ref)
	}

	// The deleted record's alias still names it, and opening it says so.
	readStore := openReadStore(t, ctx, paths.Archive)
	_, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, issued[deletedRef])
	_ = readStore.Close()
	var missing commandError
	if !errors.As(err, &missing) || missing.name != "not_found" {
		t.Fatalf("open alias of deleted record = %#v, want not_found", err)
	}
}

// retiredIdentityRef stands for a message an earlier sync indexed and a later
// source no longer holds. It never appears in a fixture, only in the index.
const retiredIdentityRef = "imessage:msg/9001"

func withWriteRequest(t *testing.T, ctx context.Context, paths trawlkit.Paths, fn func(*trawlkit.Request) error) {
	t.Helper()
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	fnErr := fn(&trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if fnErr != nil {
		t.Fatal(fnErr)
	}
}

func assignShortRefs(t *testing.T, ctx context.Context, source *Crawler, paths trawlkit.Paths) {
	t.Helper()
	withWriteRequest(t, ctx, paths, func(req *trawlkit.Request) error {
		records, err := source.ShortRefRecords(ctx, req)
		if err != nil {
			return err
		}
		_, err = req.AssignShortRefs(ctx, records)
		return err
	})
}

func shortRefAliasesFor(t *testing.T, ctx context.Context, paths trawlkit.Paths, refs []string) map[string]string {
	t.Helper()
	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	aliases, err := readRequest(readStore, paths).ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	return aliases
}

func assertShortRefResolvesTo(t *testing.T, ctx context.Context, paths trawlkit.Paths, alias, want string) {
	t.Helper()
	readStore := openReadStore(t, ctx, paths.Archive)
	defer func() { _ = readStore.Close() }()
	resolved, err := readRequest(readStore, paths).ResolveShortRef(ctx, alias)
	if err != nil {
		t.Fatalf("resolve %q: %v", alias, err)
	}
	if len(resolved) != 1 || resolved[0] != want {
		t.Fatalf("resolve %q = %#v, want %q", alias, resolved, want)
	}
}

// identityFixtureInserts is a small synthetic source: two chats, two handles
// and three messages. It carries no search or read-state shape, because the
// identity tests only care about which records exist and what they are called.
func identityFixtureInserts() []string {
	return []string{
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550110', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, 'first@example.com', 'iMessage', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'identity-chat-one', 'Sam Fixture', '+15550110', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'identity-chat-two', 'Robin Fixture', 'first@example.com', 'iMessage', '', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 2)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (1, 'identity-message-one', 1, 100, 'iMessage', 0, 'first synthetic message', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (2, 'identity-message-two', 1, 200, 'iMessage', 0, 'second synthetic message', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (3, 'identity-message-three', 2, 300, 'iMessage', 0, 'third synthetic message', null, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 2)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
	}
}

// churnedIdentityFixtureInserts is the same source after ordinary use: the
// surviving records keep their identity but are written in a different order,
// message two is gone, and a chat, a handle and two messages are new.
func churnedIdentityFixtureInserts() []string {
	return []string{
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'identity-chat-two', 'Robin Fixture', 'first@example.com', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (3, 'identity-chat-three', 'Alex Fixture', '+15550111', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'identity-chat-one', 'Sam Fixture', '+15550110', 'iMessage', '', 0)`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (3, '+15550111', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, 'first@example.com', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550110', 'iMessage', '')`,
		`insert into chat_handle_join(chat_id, handle_id) values (3, 3)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 2)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (3, 'identity-message-three', 2, 300, 'iMessage', 0, 'third synthetic message', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (5, 'identity-message-five', 3, 500, 'iMessage', 0, 'fifth synthetic message', null, 0)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (1, 'identity-message-one', 1, 100, 'iMessage', 0, 'first synthetic message', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (4, 'identity-message-four', 3, 400, 'iMessage', 0, 'fourth synthetic message', null, 0)`,
		`insert into chat_message_join(chat_id, message_id) values (3, 5)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (3, 4)`,
	}
}

func runImessageMessages(t *testing.T, ctx context.Context, source *Crawler, readStore *ckstore.Store, paths trawlkit.Paths, chat string) string {
	t.Helper()
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	source.bindMessagesFlags(fs)
	if err := fs.Parse([]string{"--chat", chat}); err != nil {
		t.Fatalf("parse messages flags: %v", err)
	}
	var out bytes.Buffer
	req := &trawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &out}
	if err := source.runMessages(ctx, req); err != nil {
		t.Fatalf("messages --chat %q failed: %v", chat, err)
	}
	return out.String()
}

func TestCrawlerSyncClassifiesArchiveUseFailureAsArchiveError(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sourcePath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	createMessagesFixture(t, sourcePath)

	paths := trawlkit.Paths{
		Archive: filepath.Join(home, ".opentrawl", appID, appID+".db"),
		Config:  filepath.Join(home, ".opentrawl", appID, "config.toml"),
		Logs:    filepath.Join(home, ".opentrawl", appID, "logs"),
	}
	initialStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := initialStore.Close(); err != nil {
		t.Fatal(err)
	}

	readOnlyStore := openReadStore(t, ctx, paths.Archive)
	_, err = New().Sync(ctx, &trawlkit.Request{
		Store:  readOnlyStore,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	})
	if closeErr := readOnlyStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil {
		t.Fatal("sync succeeded with read-only archive store")
	}
	body := ckoutput.ErrorBodyFor(err)
	if body.Code != "archive" {
		t.Fatalf("sync error code = %q, want archive; body = %#v", body.Code, body)
	}
	wantRemedy := "make the archive path writable, free disk space if needed, and fix the reported archive error"
	if body.Remedy != wantRemedy {
		t.Fatalf("sync error remedy = %q, want %q; body = %#v", body.Remedy, wantRemedy, body)
	}
}

func readRequest(st *ckstore.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func fillTestShortRefs(t *testing.T, ctx context.Context, req *trawlkit.Request, hits []trawlkit.Hit) {
	t.Helper()
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
	}
}

func openReadStore(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// messagesSourceSchema is the subset of the Apple Messages schema the crawler
// reads. Fixtures create it and then insert their own synthetic rows.
var messagesSourceSchema = []string{
	`create table handle (ROWID integer primary key, id text not null, service text not null, uncanonicalized_id text)`,
	`create table chat (ROWID integer primary key, guid text not null, display_name text, chat_identifier text, service_name text, room_name text, is_archived integer)`,
	`create table chat_handle_join (chat_id integer, handle_id integer)`,
	`create table message (ROWID integer primary key, guid text not null, handle_id integer, date integer, service text, is_from_me integer, text text, attributedBody blob, is_read integer default 0, date_read integer default 0)`,
	`create table chat_message_join (chat_id integer, message_id integer)`,
	`create table message_attachment_join (message_id integer, attachment_id integer)`,
}

// createMessagesSource writes a fresh synthetic Messages database at path,
// replacing any file already there, and applies inserts in the given order.
func createMessagesSource(t *testing.T, path string, inserts []string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range messagesSourceSchema {
		mustExec(t, db, stmt)
	}
	for _, stmt := range inserts {
		mustExec(t, db, stmt)
	}
}

func createMessagesFixture(t *testing.T, path string) {
	t.Helper()
	longLaunchNote := "latest launch note with candles budget and tariffs. " + strings.Repeat("This sentence keeps going so transcript output must stay whole. ", 3) + "full tail marker"
	inserts := []string{
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550100', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, '0015550100', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (3, 'person@example.test', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (4, '+15550103', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (5, 'opaque-handle', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (6, 'opaque123', 'SMS', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'chat-one', 'Older Name', '+15550100', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'chat-two', 'Most Recent Name', '0015550100', 'SMS', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (3, 'chat-three', 'Fixture Person', '+15550103', 'SMS', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (4, 'chat-four', '', 'group-chat', 'SMS', 'Cabinet Group', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 2)`,
		`insert into chat_handle_join(chat_id, handle_id) values (3, 4)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 4)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 5)`,
		`insert into chat_handle_join(chat_id, handle_id) values (4, 6)`,
		// is_read is set exactly as Apple sets it: 1 on a read received message,
		// 0 on an unread one, and 1 on an owner-sent message (a delivery flag,
		// which unread must ignore). chat-one has one unread received message,
		// chat-two's received message is read, message-four is unread and lands
		// in both chat-three and chat-four, and chat-four also holds an unread
		// message-five. So unread counts are chat-one 1, chat-two 0, chat-three
		// 1, chat-four 2.
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (1, 'message-one', 1, 100, 'iMessage', 0, 'older hello', null, 0)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (2, 'message-two', 2, 200, 'SMS', 0, 'earlier launch note', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (3, 'message-three', 2, 250, 'SMS', 1, '` + longLaunchNote + `', null, 1)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (4, 'message-four', 4, 300, 'SMS', 0, 'group fallback row', null, 0)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody, is_read) values (5, 'message-five', 5, 350, 'SMS', 0, 'opaque sender row', null, 0)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 2)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
		`insert into chat_message_join(chat_id, message_id) values (3, 4)`,
		`insert into chat_message_join(chat_id, message_id) values (4, 4)`,
		`insert into chat_message_join(chat_id, message_id) values (4, 5)`,
		`insert into message_attachment_join(message_id, attachment_id) values (4, 42)`,
	}
	createMessagesSource(t, path, inserts)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

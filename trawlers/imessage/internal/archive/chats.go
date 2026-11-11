package archive

import (
	"context"
	"database/sql"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

// ChatListOptions carries the read-side flags for listing chats. Limit zero
// means no cap. UnreadOnly returns only chats with at least one unread
// received message.
type ChatListOptions struct {
	Limit      int
	UnreadOnly bool
}

func (s *Store) Chats(ctx context.Context, opts ChatListOptions) ([]ChatSummary, error) {
	db := s.store.DB()
	unreadSelect := unreadReceivedExpr
	unreadFilter := ""
	if opts.UnreadOnly {
		unreadFilter = "and unread > 0"
	}
	limitClause := ""
	args := []any{}
	if opts.Limit > 0 {
		limitClause = "limit ?"
		args = append(args, opts.Limit)
	}
	rows, err := db.QueryContext(ctx, chatSummaryQuery("", unreadSelect, unreadFilter)+limitClause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out, err := scanChatSummaries(rows)
	if err != nil {
		return nil, err
	}
	if err := populateConversationParticipantIdentities(ctx, db, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Chat(ctx context.Context, chatID ChatID) (ChatSummary, error) {
	return s.readChatSummary(ctx, "where c.chat_id = ?", chatID)
}

func (s *Store) readChatSummary(ctx context.Context, where string, chatKey any) (ChatSummary, error) {
	db := s.store.DB()
	// The single-chat read backs the messages verb header, which never shows an
	// unread count, so the unread select is null and Chat.Unread stays nil here.
	rows, err := db.QueryContext(ctx, chatSummaryQuery(where, "null", "")+"limit 1", chatKey)
	if err != nil {
		return ChatSummary{}, err
	}
	defer func() { _ = rows.Close() }()
	out, err := scanChatSummaries(rows)
	if err != nil {
		return ChatSummary{}, err
	}
	if len(out) == 0 {
		return ChatSummary{}, ErrChatNotFound
	}
	if err := populateConversationParticipantIdentities(ctx, db, out); err != nil {
		return ChatSummary{}, err
	}
	return out[0], nil
}

func scanChatSummaries(rows *sql.Rows) ([]ChatSummary, error) {
	out := []ChatSummary{}
	for rows.Next() {
		var c ChatSummary
		var unread sql.NullInt64
		if err := rows.Scan(&c.chatRowID, &c.ChatID, &c.GUID, &c.Title, &c.Kind, &c.ChatIdentifier, &c.RoomName, &c.Service, &c.ParticipantCount, &c.MessageCount, &c.LatestMessageDate, &unread); err != nil {
			return nil, err
		}
		if unread.Valid {
			count := unread.Int64
			c.Unread = &count
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func populateConversationParticipantIdentities(ctx context.Context, db *sql.DB, chats []ChatSummary) error {
	for i := range chats {
		conversationParticipantIdentities, err := readConversationParticipantIdentities(
			ctx,
			db,
			chats[i].chatRowID,
		)
		if err != nil {
			return err
		}
		chats[i].ConversationParticipantIdentities = conversationParticipantIdentities
	}
	return nil
}

func readConversationParticipantIdentities(
	ctx context.Context,
	db *sql.DB,
	chatRowID int64,
) ([]ConversationParticipantIdentity, error) {
	rows, err := db.QueryContext(ctx, conversationParticipantIdentitiesSQL, chatRowID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var conversationParticipantIdentities []ConversationParticipantIdentity
	for rows.Next() {
		var conversationParticipantIdentity ConversationParticipantIdentity
		var exactPersonFilterIdentifier string
		if err := rows.Scan(
			&exactPersonFilterIdentifier,
			&conversationParticipantIdentity.PersonDisplayName,
		); err != nil {
			return nil, err
		}
		conversationParticipantIdentity.ExactPersonFilterIdentifier = &person.ExactPersonFilterIdentifier{
			ExactPersonFilterIdentifier: exactPersonFilterIdentifier,
		}
		conversationParticipantIdentities = append(
			conversationParticipantIdentities,
			conversationParticipantIdentity,
		)
	}
	return conversationParticipantIdentities, rows.Err()
}

package archive

import "github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"

const (
	MessageRefPrefix = "imessage:msg/"
	// ChatRefPrefix is the provider-native prefix for a canonical conversation record.
	ChatRefPrefix = "imessage:chat/"
)

func MessageRef(messageGUID messages.MessageGUID) string {
	return MessageRefPrefix + string(messageGUID)
}

type ChatID string

func chatID(chat messages.Chat) ChatID {
	if chat.CloudKitRecordID != nil && *chat.CloudKitRecordID != "" {
		return ChatID(*chat.CloudKitRecordID)
	}
	return ChatID(chat.GUID)
}

func ChatRef(chatID ChatID) string {
	if chatID == "" {
		return ""
	}
	return ChatRefPrefix + string(chatID)
}

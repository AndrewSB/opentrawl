package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
)

func main() {
	archivePath := flag.String("archive", "", "synthetic Messages archive path")
	flag.Parse()
	if flag.NArg() != 0 || strings.TrimSpace(*archivePath) == "" {
		fmt.Fprintln(os.Stderr, "usage: seed --archive PATH")
		os.Exit(2)
	}
	ctx := context.Background()
	st, err := archive.Open(ctx, *archivePath)
	if err == nil {
		err = st.ReplaceAll(ctx, messages.ArchiveData{
			SourcePath:       "/synthetic/Messages/chat.db",
			SourceBytes:      1,
			SourceModifiedAt: time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
			ExtractedAt:      time.Date(2026, 7, 9, 10, 1, 0, 0, time.UTC),
			Handles:          []messages.Handle{{SourceRowID: 1, ID: "avery@example.com", Service: "iMessage", DisplayName: "Avery Example"}},
			Chats:            []messages.Chat{{SourceRowID: 1, GUID: "synthetic-chat", ChatIdentifier: "avery@example.com", ServiceName: "iMessage", DisplayName: "Example Plans"}},
			Participants:     []messages.Participant{{ChatRowID: 1, HandleRowID: 1}},
			ChatMessages:     []messages.ChatMessage{{ChatRowID: 1, MessageRowID: 1}},
			Messages:         []messages.Message{{SourceRowID: 1, GUID: "synthetic-message", HandleRowID: 1, Date: 700000000000000000, Service: "iMessage", Text: "synthetic lantern launch plan", IsRead: true}},
		}, nil, nil, time.Date(2026, 7, 9, 10, 2, 0, 0, time.UTC))
	}
	if st != nil {
		if closeErr := st.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

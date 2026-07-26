package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	wastore "github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
)

func main() {
	archivePath := flag.String("archive", "", "synthetic WhatsApp archive path")
	flag.Parse()
	if flag.NArg() != 0 || strings.TrimSpace(*archivePath) == "" {
		fmt.Fprintln(os.Stderr, "usage: seed --archive PATH")
		os.Exit(2)
	}
	ctx := context.Background()
	st, err := wastore.Open(ctx, *archivePath)
	if err == nil {
		at := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
		err = st.ReplaceAll(ctx,
			wastore.ImportStats{SourcePath: "/synthetic/WhatsApp", StartedAt: at, FinishedAt: at},
			[]wastore.Contact{{JID: "avery@example.com", FullName: "Avery Example", UpdatedAt: at}},
			[]wastore.Chat{{JID: "synthetic-chat@g.us", Kind: "group", Name: "Example Plans", LastMessageAt: at}},
			[]wastore.Group{{JID: "synthetic-chat@g.us", Name: "Example Plans", CreatedAt: at}},
			[]wastore.GroupParticipant{{GroupJID: "synthetic-chat@g.us", UserJID: "avery@example.com", ContactName: "Avery Example", IsActive: true}},
			[]wastore.Message{{SourcePK: 1, ChatJID: "synthetic-chat@g.us", ChatName: "Example Plans", MessageID: "synthetic-message", SenderJID: "avery@example.com", SenderName: "Avery Example", Timestamp: at, Text: "synthetic lantern launch plan"}},
		)
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

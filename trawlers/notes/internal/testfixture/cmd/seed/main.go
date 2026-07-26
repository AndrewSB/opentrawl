package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

func main() {
	archivePath := flag.String("archive", "", "synthetic Notes archive path")
	flag.Parse()
	if flag.NArg() != 0 || strings.TrimSpace(*archivePath) == "" {
		fmt.Fprintln(os.Stderr, "usage: seed --archive PATH")
		os.Exit(2)
	}
	ctx := context.Background()
	at := "2026-07-09T10:00:00Z"
	body, err := noteBody("synthetic lantern launch plan")
	st, openErr := archive.Open(ctx, *archivePath)
	if err == nil {
		err = openErr
	}
	if err == nil {
		_, err = st.ApplySync(ctx, archive.SyncBatch{
			Notes: []archive.Note{{ID: "synthetic-note", Title: "Example Plan", Folder: "Examples", CreatedAt: at, ModifiedAt: at}},
			Bodies: []archive.BodyInsert{{
				NoteID: "synthetic-note", ZDataSHA256: archive.SHA256(body), ZData: body,
				Source: "synthetic", SourceDetail: "fixture", SourceModifiedAt: at, ObservedAt: at, Title: "Example Plan",
			}},
			SyncState:  map[string]string{"last_sync_at": at},
			LastSeenAt: at, RefreshNoteMetadata: true,
		})
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

func noteBody(text string) ([]byte, error) {
	note := protoField(2, []byte(text))
	document := protoField(3, note)
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(protoField(2, document)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func protoField(field int, data []byte) []byte {
	var encoded [10]byte
	keyLength := binary.PutUvarint(encoded[:], uint64(field<<3|2))
	out := append([]byte{}, encoded[:keyLength]...)
	valueLength := binary.PutUvarint(encoded[:], uint64(len(data)))
	out = append(out, encoded[:valueLength]...)
	return append(out, data...)
}

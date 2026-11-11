package archive

import (
	"context"
	"database/sql"
	"os"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `select count(*) from `+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func ftsQuery(query string) string {
	value, err := ckstore.FTS5Terms(query, "")
	if err != nil {
		return ckstore.FTS5Phrase(query)
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func AppleDateTime(value int64) time.Time {
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return epoch.Add(time.Duration(value)).Local()
}

func AppleDateFromTime(value time.Time) int64 {
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return int64(value.UTC().Sub(epoch))
}

func FormatAppleDateTime(value int64) string {
	if value <= 0 {
		return ""
	}
	return AppleDateTime(value).Format(time.RFC3339)
}

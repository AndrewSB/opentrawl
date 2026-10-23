//go:build !cgo

package cache

import (
	"context"
	"database/sql"
	"errors"
)

// backupSQLiteDatabase needs SQLite's online backup API, which the driver only
// exposes when cgo is compiled in. Without cgo the driver cannot open a
// database either, so this is unreachable in a working build; it exists so that
// shared packages still compile for a target whose toolchain has no C
// compiler, which is what makes `scripts/check-linux` runnable from a Mac.
func backupSQLiteDatabase(ctx context.Context, targetDB, sourceDB *sql.DB) error {
	_, _, _ = ctx, targetDB, sourceDB
	return errors.New("sqlite snapshots require a cgo build of OpenTrawl")
}

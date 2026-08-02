//go:build !darwin

package photos

import (
	"context"
	"errors"
)

// PhotoLibraryAccessStatusThroughApp completes the non-Darwin half of this
// file's contract. Its Darwin counterpart is exported, so a caller added on a
// Mac compiles there and breaks the Linux build instead of failing at the
// platform boundary like every other unsupported operation here.
func PhotoLibraryAccessStatusThroughApp(ctx context.Context, request bool) (string, error) {
	return "", errors.New("signed Photos access status requires macOS")
}

func ExportOriginalResourceThroughApp(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	return errors.New("signed Photos original fetch app requires macOS")
}

func AssetReadinessThroughApp(ctx context.Context, assetUUID string) (AssetReadiness, error) {
	return AssetReadiness{}, errors.New("signed Photos asset readiness requires macOS")
}

func ExportCurrentStillThroughApp(ctx context.Context, request CurrentStillRequest, destinationPath string) (CurrentStillFact, error) {
	return CurrentStillFact{}, errors.New("signed Photos current still export requires macOS")
}

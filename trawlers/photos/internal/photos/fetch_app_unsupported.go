//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func ExportOriginalResourceThroughApp(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	return errors.New("signed Photos original fetch app requires macOS")
}

func AssetReadinessThroughApp(ctx context.Context, assetUUID string) (AssetReadiness, error) {
	return AssetReadiness{}, errors.New("signed Photos asset readiness requires macOS")
}

func ExportCurrentStillThroughApp(ctx context.Context, request CurrentStillRequest, destinationPath string) (CurrentStillFact, error) {
	return CurrentStillFact{}, errors.New("signed Photos current-still export requires macOS")
}

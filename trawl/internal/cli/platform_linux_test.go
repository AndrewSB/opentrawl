//go:build linux

package cli

import (
	"encoding/json"
	"testing"
)

func TestLinuxRejectsAcquisitionWithStructuredError(t *testing.T) {
	previous := acquisitionSupported
	acquisitionSupported = platformAcquisitionSupported
	t.Cleanup(func() { acquisitionSupported = previous })
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "sync", "imessage")
	if code != 1 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode error: %v\n%s", err, stdout)
	}
	if envelope.Error.Code != "acquisition_unsupported" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestLinuxRejectsMutatingSourceVerbWithStructuredError(t *testing.T) {
	previous := acquisitionSupported
	acquisitionSupported = platformAcquisitionSupported
	t.Cleanup(func() { acquisitionSupported = previous })
	writeFakeCrawlers(t, fakeCrawler{
		name:     "notes",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"notes","display_name":"Notes","commands":{"sync_store":{"argv":["notes","sync-store","PATH"],"json":true,"mutates":true}}}`,
	})
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "notes", "sync-store", "/tmp/synthetic.db")
	if code != 1 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode error: %v\n%s", err, stdout)
	}
	if envelope.Error.Code != "read_only_platform" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

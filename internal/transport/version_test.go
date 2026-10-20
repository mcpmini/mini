package transport

import (
	"encoding/json"
	"testing"
)

func TestIsLegacyVersion(t *testing.T) {
	for _, version := range LegacyVersions {
		if !IsLegacyVersion(version) {
			t.Errorf("IsLegacyVersion(%q) = false, want true", version)
		}
	}
	for _, version := range []string{"1999-01-01", "", "2026-07-28"} {
		if IsLegacyVersion(version) {
			t.Errorf("IsLegacyVersion(%q) = true, want false", version)
		}
	}
}

func TestProtocolVersionFromParams(t *testing.T) {
	version := ProtocolVersionFromParams(json.RawMessage(
		`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`,
	))
	if version != "2026-07-28" {
		t.Fatalf("version=%q", version)
	}
	for _, params := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`not-json`)} {
		if version := ProtocolVersionFromParams(params); version != "" {
			t.Errorf("params=%s version=%q, want none", params, version)
		}
	}
}

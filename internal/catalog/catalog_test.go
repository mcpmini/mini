package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	catalogdata "github.com/mcpmini/mini/catalog"
)

func TestLoad(t *testing.T) {
	var published struct{ Entries []json.RawMessage }
	if err := json.Unmarshal(catalogdata.V1(), &published); err != nil {
		t.Fatal(err)
	}
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(published.Entries) {
		t.Errorf("entries = %d, want %d", len(entries), len(published.Entries))
	}
}

func catalogJSON(t *testing.T, schemaVersion int, entries ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schema_version": schemaVersion, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validEntry(change func(map[string]any)) map[string]any {
	entry := map[string]any{"name": "example", "url": "https://example.com/mcp", "description": "d", "category": "c", "auth": "none"}
	change(entry)
	return entry
}

func TestParseAcceptsAValidEntry(t *testing.T) {
	entries, err := parse(catalogJSON(t, 1, validEntry(func(map[string]any) {})))
	if err != nil || len(entries) != 1 || entries[0].Name != "example" {
		t.Fatalf("parse = %v, %v; want the one entry", entries, err)
	}
}

func TestParseAcceptsTextAtTheRunesLimit(t *testing.T) {
	description := strings.Repeat("é", maxTextRunes)
	entries, err := parse(catalogJSON(t, 1, validEntry(func(entry map[string]any) {
		entry["description"] = description
	})))
	if err != nil || len(entries) != 1 || entries[0].Description != description {
		t.Fatalf("parse = %v, %v; want the entry with %d-rune description", entries, err, maxTextRunes)
	}
}

// Validation errors are printed to the terminal, so they must never carry a raw control character.
func assertParseError(t *testing.T, data []byte, want string) {
	t.Helper()
	_, err := parse(data)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("parse error = %v, want %q", err, want)
	}
	if strings.ContainsFunc(err.Error(), unicode.IsControl) {
		t.Errorf("parse error %q carries raw control characters to the terminal", err)
	}
}

func TestParseRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"bad name", func(e map[string]any) { e["name"] = "bad name" }, `invalid name "bad name"`},
		{"missing name", func(e map[string]any) { delete(e, "name") }, "catalog entry 1: name is required"},
		{"missing url", func(e map[string]any) { delete(e, "url") }, `catalog entry "example": url is required`},
		{"http url", func(e map[string]any) { e["url"] = "http://example.com/mcp" }, `catalog entry "example": url must be an https URL`},
		{"non-ASCII host", func(e map[string]any) { e["url"] = "https://g\u0456thub.com/mcp" }, `catalog entry "example": url host must be ASCII`},
		{"missing description", func(e map[string]any) { delete(e, "description") }, `catalog entry "example": description is required`},
		{"blank category", func(e map[string]any) { e["category"] = " " }, `catalog entry "example": category is required`},
		{"control character", func(e map[string]any) { e["description"] = "hi\x1b[2J" }, `catalog entry "example": description contains control characters`},
		{"text direction override", func(e map[string]any) { e["description"] = "safe\u202etxt.exe" }, `catalog entry "example": description contains control characters`},
		{"description too long in runes", func(e map[string]any) { e["description"] = strings.Repeat("é", maxTextRunes+1) }, `catalog entry "example": description is longer than 120 characters`},
		{"consecutive spaces", func(e map[string]any) { e["description"] = "a  b" }, `catalog entry "example": description has irregular spacing`},
		{"tab spacing", func(e map[string]any) { e["description"] = "a\tb" }, `catalog entry "example": description has irregular spacing`},
		{"trailing category space", func(e map[string]any) { e["category"] = "category " }, `catalog entry "example": category has irregular spacing`},
		{"ideographic space", func(e map[string]any) { e["description"] = "a\u3000b" }, `catalog entry "example": description has irregular spacing`},
		{"escape sequence in name", func(e map[string]any) { e["name"] = "\x1b[2J" }, "invalid name"},
		{"unknown auth", func(e map[string]any) { e["auth"] = "magic" }, `catalog entry "example": invalid auth "magic"`},
		{"escape sequence in auth", func(e map[string]any) { e["auth"] = "bad\x1b" }, "invalid auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertParseError(t, catalogJSON(t, 1, validEntry(tt.change)), tt.want)
		})
	}
}

func TestParseRejectsInvalidDocuments(t *testing.T) {
	same := validEntry(func(map[string]any) {})
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"malformed JSON", []byte(`{"schema_version":1,"entries":[`), "parse catalog"},
		{"unknown schema version", catalogJSON(t, 2, same), "schema_version must be 1"},
		{"no entries", catalogJSON(t, 1), "catalog entries are required"},
		{"duplicate name", catalogJSON(t, 1, same, same), `catalog entry "example": duplicate name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertParseError(t, tt.data, tt.want)
		})
	}
}

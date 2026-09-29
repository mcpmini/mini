package catalog

import (
	"encoding/json"
	"strings"
	"testing"

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

func TestParseRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"bad name", func(e map[string]any) { e["name"] = "bad name" }, `invalid name "bad name"`},
		{"missing name", func(e map[string]any) { delete(e, "name") }, "catalog entry 1: name is required"},
		{"missing url", func(e map[string]any) { delete(e, "url") }, "catalog entry example: url is required"},
		{"http url", func(e map[string]any) { e["url"] = "http://example.com/mcp" }, "catalog entry example: url must be an https URL"},
		{"missing description", func(e map[string]any) { delete(e, "description") }, "catalog entry example: description and category are required"},
		{"blank category", func(e map[string]any) { e["category"] = " " }, "catalog entry example: description and category are required"},
		{"unknown auth", func(e map[string]any) { e["auth"] = "magic" }, `catalog entry example: invalid auth "magic"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(catalogJSON(t, 1, validEntry(tt.change)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse error = %v, want %q", err, tt.want)
			}
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
		{"duplicate name", catalogJSON(t, 1, same, same), "catalog entry example: duplicate name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse error = %v, want %q", err, tt.want)
			}
		})
	}
}

package catalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	catalogdata "github.com/mcpmini/mini/catalog"
)

func TestLoad(t *testing.T) {
	var published struct {
		Entries []Entry
		Popular []string
	}
	if err := json.Unmarshal(catalogdata.V1(), &published); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Entries, published.Entries) {
		t.Errorf("entries changed while loading: got %+v, want %+v", c.Entries, published.Entries)
	}
	wantPopular := []string{"github", "slack", "atlassian", "notion", "linear", "datadog", "sentry"}
	if !slices.Equal(c.Popular, wantPopular) {
		t.Errorf("popular = %v, want %v", c.Popular, wantPopular)
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
	entry := map[string]any{"name": "example", "title": "Example", "url": "https://example.com/mcp", "description": "d", "category": "c", "auth": "none"}
	change(entry)
	return entry
}

func TestParseAcceptsAValidEntry(t *testing.T) {
	c, err := parse(catalogJSON(t, 1, validEntry(func(map[string]any) {})))
	if err != nil || len(c.Entries) != 1 || c.Entries[0].Name != "example" {
		t.Fatalf("parse = %v, %v; want the one entry", c, err)
	}
}

func TestParseAcceptsTextAtTheRunesLimit(t *testing.T) {
	description := strings.Repeat("é", maxTextRunes)
	c, err := parse(catalogJSON(t, 1, validEntry(func(entry map[string]any) {
		entry["description"] = description
	})))
	if err != nil || len(c.Entries) != 1 || c.Entries[0].Description != description {
		t.Fatalf("parse = %v, %v; want the entry with %d-rune description", c, err, maxTextRunes)
	}
}

func assertTerminalSafeParseError(t *testing.T, data []byte, want string) {
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
		{"hidden setup URL formatting", func(e map[string]any) { e["auth"], e["setup_url"] = "token", "https://example.com/\u202egithub.com" }, `catalog entry "example": setup_url: url must be printable ASCII`},
		{"hidden URL formatting", func(e map[string]any) { e["url"] = "https://example.com/\u200b" }, `catalog entry "example": url must be printable ASCII`},
		{"space in setup URL", func(e map[string]any) { e["auth"], e["setup_url"] = "token", "https://example.com/token page" }, `catalog entry "example": setup_url: url must be printable ASCII`},
		{"lookalike percent-encoded non-ASCII host", func(e map[string]any) { e["url"] = "https://g%D1%96thub.com/mcp" }, `catalog entry "example": url host must be ASCII`},
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
		{"missing title", func(e map[string]any) { delete(e, "title") }, `catalog entry "example": title is required`},
		{"host-like brackets in title", func(e map[string]any) { e["title"] = "Linear [mcp.linear.app]" }, `catalog entry "example": title must be at most 40 characters without brackets`},
		{"title too long", func(e map[string]any) { e["title"] = strings.Repeat("é", maxTitleRunes+1) }, `catalog entry "example": title must be at most 40 characters without brackets`},
		{"token without setup_url", func(e map[string]any) { e["auth"] = "token" }, `catalog entry "example": setup_url: url must be an https URL`},
		{"http setup_url", func(e map[string]any) { e["auth"], e["setup_url"] = "oauth2-app", "http://example.com/apps" }, `catalog entry "example": setup_url: url must be an https URL`},
		{"setup_url on an oauth2 entry", func(e map[string]any) { e["auth"], e["setup_url"] = "oauth2", "https://example.com/apps" }, `catalog entry "example": setup_url is only for token and oauth2-app entries`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTerminalSafeParseError(t, catalogJSON(t, 1, validEntry(tt.change)), tt.want)
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
			assertTerminalSafeParseError(t, tt.data, tt.want)
		})
	}
}

func catalogWithPopular(t *testing.T, popular []string, entries ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schema_version": 1, "entries": entries, "popular": popular})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParsePopular_preservesIDOrderAndFlatEntries(t *testing.T) {
	first := validEntry(func(e map[string]any) { e["name"], e["category"] = "first", "Zeta" })
	second := validEntry(func(e map[string]any) { e["name"], e["category"] = "second", "Alpha" })
	c, err := parse(catalogWithPopular(t, []string{"second", "first"}, first, second))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Popular, []string{"second", "first"}) || c.Entries[0].Name != "first" || c.Entries[1].Name != "second" {
		t.Fatalf("catalog = %+v, want popular [second first] and entries [first second]", c)
	}
	if c.Entries[0].Category != "Zeta" || c.Entries[1].Category != "Alpha" {
		t.Fatalf("category metadata changed: %+v", c.Entries)
	}
}

func TestParsePopular_missingOrEmptyIsAccepted(t *testing.T) {
	entry := validEntry(func(map[string]any) {})
	for _, data := range [][]byte{catalogJSON(t, 1, entry), catalogWithPopular(t, []string{}, entry)} {
		c, err := parse(data)
		if err != nil || len(c.Popular) != 0 || len(c.Entries) != 1 {
			t.Fatalf("parse = %+v, %v; want one entry and no popular IDs", c, err)
		}
	}
}

func TestParsePopular_unknownOrRepeatedIDIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name    string
		popular []string
		want    string
	}{
		{"unknown", []string{"missing"}, `"missing" is not a catalog server`},
		{"duplicate", []string{"example", "example"}, `"example" is listed twice`},
		{"control characters", []string{"bad\x1b"}, "not a catalog server"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertTerminalSafeParseError(t, catalogWithPopular(t, tt.popular, validEntry(func(map[string]any) {})), tt.want)
		})
	}
}

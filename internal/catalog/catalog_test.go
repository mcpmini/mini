package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode"

	catalogdata "github.com/mcpmini/mini/catalog"
)

func TestLoadKeepsThePublishedOrderAndPopularServers(t *testing.T) {
	var published struct {
		Popular    []string
		Categories []struct {
			Title   string
			Servers []struct{ Name string }
		}
	}
	if err := json.Unmarshal(catalogdata.V1(), &published); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Popular, published.Popular) || len(c.Popular) == 0 {
		t.Errorf("popular = %v, want %v", c.Popular, published.Popular)
	}
	var wantNames, wantTitles []string
	for _, category := range published.Categories {
		wantTitles = append(wantTitles, category.Title)
		for _, server := range category.Servers {
			wantNames = append(wantNames, server.Name)
		}
	}
	if titles := categoryTitles(c); !slices.Equal(titles, wantTitles) {
		t.Errorf("categories = %v, want %v", titles, wantTitles)
	}
	if names := entryNames(c.Entries()); !slices.Equal(names, wantNames) {
		t.Errorf("servers = %v, want %v", names, wantNames)
	}
}

func categoryTitles(c Catalog) []string {
	var titles []string
	for _, category := range c.Categories {
		titles = append(titles, category.Title)
	}
	return titles
}

func entryNames(entries []Entry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func catalogJSON(t *testing.T, schemaVersion int, entries ...map[string]any) []byte {
	t.Helper()
	return documentJSON(t, map[string]any{"schema_version": schemaVersion, "categories": []any{category("c", entries...)}})
}

func documentJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func category(title string, servers ...map[string]any) map[string]any {
	if servers == nil {
		servers = []map[string]any{}
	}
	return map[string]any{"title": title, "servers": servers}
}

func validEntry(change func(map[string]any)) map[string]any {
	entry := map[string]any{"name": "example", "title": "Example", "url": "https://example.com/mcp", "description": "d", "auth": "none"}
	change(entry)
	return entry
}

func named(name string) map[string]any {
	return validEntry(func(e map[string]any) { e["name"] = name })
}

func TestParseAcceptsAValidEntry(t *testing.T) {
	c, err := parse(catalogJSON(t, 1, validEntry(func(map[string]any) {})))
	if err != nil || len(c.Entries()) != 1 || c.Entries()[0].Name != "example" {
		t.Fatalf("parse = %v, %v; want the one entry", c, err)
	}
}

func TestParseKeepsCategoryAndServerOrder(t *testing.T) {
	c, err := parse(documentJSON(t, map[string]any{
		"schema_version": 1,
		"popular":        []string{"c", "a"},
		"categories":     []any{category("Zeta", named("b"), named("a")), category("Alpha", named("c"))},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if titles, names := categoryTitles(c), entryNames(c.Entries()); !slices.Equal(titles, []string{"Zeta", "Alpha"}) || !slices.Equal(names, []string{"b", "a", "c"}) {
		t.Errorf("parse order = %v %v, want [Zeta Alpha] [b a c]", titles, names)
	}
	if !slices.Equal(c.Popular, []string{"c", "a"}) {
		t.Errorf("popular = %v, want [c a]", c.Popular)
	}
}

func TestParseAcceptsTextAtTheRunesLimit(t *testing.T) {
	description := strings.Repeat("é", maxTextRunes)
	c, err := parse(catalogJSON(t, 1, validEntry(func(entry map[string]any) {
		entry["description"] = description
	})))
	if err != nil || len(c.Entries()) != 1 || c.Entries()[0].Description != description {
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
		{"control character", func(e map[string]any) { e["description"] = "hi\x1b[2J" }, `catalog entry "example": description contains control characters`},
		{"text direction override", func(e map[string]any) { e["description"] = "safe\u202etxt.exe" }, `catalog entry "example": description contains control characters`},
		{"description too long in runes", func(e map[string]any) { e["description"] = strings.Repeat("é", maxTextRunes+1) }, `catalog entry "example": description is longer than 120 characters`},
		{"consecutive spaces", func(e map[string]any) { e["description"] = "a  b" }, `catalog entry "example": description has irregular spacing`},
		{"tab spacing", func(e map[string]any) { e["description"] = "a\tb" }, `catalog entry "example": description has irregular spacing`},
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
		{"malformed JSON", []byte(`{"schema_version":1,"categories":[`), "parse catalog"},
		{"unknown schema version", catalogJSON(t, 2, same), "schema_version must be 1"},
		{"no categories", documentJSON(t, map[string]any{"schema_version": 1}), "catalog categories are required"},
		{"old flat entries format", documentJSON(t, map[string]any{"schema_version": 1, "entries": []any{same}}), "catalog categories are required"},
		{"empty category", documentJSON(t, map[string]any{"schema_version": 1, "categories": []any{category("Empty")}}), `catalog category "Empty": has no servers`},
		{"blank category title", documentJSON(t, map[string]any{"schema_version": 1, "categories": []any{category(" ", same)}}), `catalog category " ": title is required`},
		{"category title with escape", documentJSON(t, map[string]any{"schema_version": 1, "categories": []any{category("Dev\x1b[2J", same)}}), "title contains control characters"},
		{"duplicate name", catalogJSON(t, 1, same, same), `catalog entry "example": duplicate name`},
		{"server in two categories", documentJSON(t, map[string]any{"schema_version": 1, "categories": []any{category("A", same), category("B", same)}}), `catalog entry "example": duplicate name`},
		{"unknown popular name", documentJSON(t, map[string]any{"schema_version": 1, "popular": []string{"missing"}, "categories": []any{category("A", same)}}), `catalog popular: "missing" is not a catalog server`},
		{"repeated popular name", documentJSON(t, map[string]any{"schema_version": 1, "popular": []string{"example", "example"}, "categories": []any{category("A", same)}}), `catalog popular: "example" is listed twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTerminalSafeParseError(t, tt.data, tt.want)
		})
	}
}

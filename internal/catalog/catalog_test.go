package catalog

import (
	"cmp"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	catalogdata "github.com/mcpmini/mini/catalog"
)

type publishedCatalog struct {
	Entries       []Entry
	Popular       []string
	CategoryOrder []string `json:"category_order"`
}

func loadPublished(t *testing.T) publishedCatalog {
	t.Helper()
	var published publishedCatalog
	if err := json.Unmarshal(catalogdata.V1(), &published); err != nil {
		t.Fatal(err)
	}
	return published
}

func entriesSortedByName(entries []Entry) []Entry {
	return slices.SortedFunc(slices.Values(entries), func(a, b Entry) int { return cmp.Compare(a.Name, b.Name) })
}

func TestLoad(t *testing.T) {
	published := loadPublished(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entriesSortedByName(c.Entries), entriesSortedByName(published.Entries)) {
		t.Errorf("loading changed the set of entries")
	}
	wantPopular := []string{"github", "slack", "atlassian", "notion", "linear", "datadog", "sentry"}
	if !slices.Equal(c.Popular, wantPopular) {
		t.Errorf("popular = %v, want %v", c.Popular, wantPopular)
	}
	wantCategoryOrder := []string{
		"Developer tools", "Productivity & collaboration", "Project management", "Observability",
	}
	if !slices.Equal(c.CategoryOrder, wantCategoryOrder) {
		t.Errorf("category_order = %v, want %v", c.CategoryOrder, wantCategoryOrder)
	}
}

func TestLoadShowsListedCategoriesFirstThenUnlistedInFirstAppearanceOrder(t *testing.T) {
	published := loadPublished(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var unlisted []string
	for _, entry := range published.Entries {
		if !slices.Contains(c.CategoryOrder, entry.Category) && !slices.Contains(unlisted, entry.Category) {
			unlisted = append(unlisted, entry.Category)
		}
	}
	wantCategories := append(slices.Clone(c.CategoryOrder), unlisted...)
	gotCategories := distinctCategoryRuns(t, c.Entries)
	if !slices.Equal(gotCategories, wantCategories) {
		t.Fatalf("category runs = %v, want %v", gotCategories, wantCategories)
	}
	for _, category := range wantCategories {
		if !reflect.DeepEqual(entriesInCategory(c.Entries, category), entriesInCategory(published.Entries, category)) {
			t.Errorf("entries of %q are not in published order", category)
		}
	}
}

// distinctCategoryRuns fails when a category's entries are not contiguous.
func distinctCategoryRuns(t *testing.T, entries []Entry) []string {
	t.Helper()
	var runs []string
	for _, entry := range entries {
		if len(runs) == 0 || runs[len(runs)-1] != entry.Category {
			runs = append(runs, entry.Category)
		}
	}
	if len(runs) != len(uniqueCategories(entries)) {
		t.Fatalf("a category's entries are split across the list: runs %v", runs)
	}
	return runs
}

func uniqueCategories(entries []Entry) []string {
	var categories []string
	for _, entry := range entries {
		if !slices.Contains(categories, entry.Category) {
			categories = append(categories, entry.Category)
		}
	}
	return categories
}

func entriesInCategory(entries []Entry, category string) []Entry {
	return slices.DeleteFunc(slices.Clone(entries), func(entry Entry) bool { return entry.Category != category })
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
	entry := map[string]any{
		"name":        "example",
		"title":       "Example",
		"url":         "https://example.com/mcp",
		"description": "d",
		"category":    "c",
		"auth":        "none",
	}
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
		{
			"http url",
			func(e map[string]any) { e["url"] = "http://example.com/mcp" },
			`catalog entry "example": url must be an https URL`,
		},
		{
			"hidden setup URL formatting",
			func(e map[string]any) { e["auth"], e["setup_url"] = "token", "https://example.com/\u202egithub.com" },
			`catalog entry "example": setup_url: url must be printable ASCII`,
		},
		{
			"hidden URL formatting",
			func(e map[string]any) { e["url"] = "https://example.com/\u200b" },
			`catalog entry "example": url must be printable ASCII`,
		},
		{
			"space in setup URL",
			func(e map[string]any) { e["auth"], e["setup_url"] = "token", "https://example.com/token page" },
			`catalog entry "example": setup_url: url must be printable ASCII`,
		},
		{
			"lookalike percent-encoded non-ASCII host",
			func(e map[string]any) { e["url"] = "https://g%D1%96thub.com/mcp" },
			`catalog entry "example": url host must be ASCII`,
		},
		{
			"missing description",
			func(e map[string]any) { delete(e, "description") },
			`catalog entry "example": description is required`,
		},
		{
			"blank category",
			func(e map[string]any) { e["category"] = " " },
			`catalog entry "example": category is required`,
		},
		{
			"control character",
			func(e map[string]any) { e["description"] = "hi\x1b[2J" },
			`catalog entry "example": description contains control characters`,
		},
		{
			"text direction override",
			func(e map[string]any) { e["description"] = "safe\u202etxt.exe" },
			`catalog entry "example": description contains control characters`,
		},
		{
			"description too long in runes",
			func(e map[string]any) { e["description"] = strings.Repeat("é", maxTextRunes+1) },
			`catalog entry "example": description is longer than 120 characters`,
		},
		{
			"consecutive spaces",
			func(e map[string]any) { e["description"] = "a  b" },
			`catalog entry "example": description has irregular spacing`,
		},
		{
			"tab spacing",
			func(e map[string]any) { e["description"] = "a\tb" },
			`catalog entry "example": description has irregular spacing`,
		},
		{
			"trailing category space",
			func(e map[string]any) { e["category"] = "category " },
			`catalog entry "example": category has irregular spacing`,
		},
		{
			"ideographic space",
			func(e map[string]any) { e["description"] = "a\u3000b" },
			`catalog entry "example": description has irregular spacing`,
		},
		{"escape sequence in name", func(e map[string]any) { e["name"] = "\x1b[2J" }, "invalid name"},
		{
			"unknown auth",
			func(e map[string]any) { e["auth"] = "magic" },
			`catalog entry "example": invalid auth "magic"`,
		},
		{"escape sequence in auth", func(e map[string]any) { e["auth"] = "bad\x1b" }, "invalid auth"},
		{"missing title", func(e map[string]any) { delete(e, "title") }, `catalog entry "example": title is required`},
		{
			"host-like brackets in title",
			func(e map[string]any) { e["title"] = "Linear [mcp.linear.app]" },
			`catalog entry "example": title must be at most 40 characters without brackets`,
		},
		{
			"title too long",
			func(e map[string]any) { e["title"] = strings.Repeat("é", maxTitleRunes+1) },
			`catalog entry "example": title must be at most 40 characters without brackets`,
		},
		{
			"token without setup_url",
			func(e map[string]any) { e["auth"] = "token" },
			`catalog entry "example": setup_url: url must be an https URL`,
		},
		{
			"http setup_url",
			func(e map[string]any) { e["auth"], e["setup_url"] = "oauth2-app", "http://example.com/apps" },
			`catalog entry "example": setup_url: url must be an https URL`,
		},
		{
			"setup_url on an oauth2 entry",
			func(e map[string]any) { e["auth"], e["setup_url"] = "oauth2", "https://example.com/apps" },
			`catalog entry "example": setup_url is only for token and oauth2-app entries`,
		},
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
	if !slices.Equal(c.Popular, []string{"second", "first"}) || c.Entries[0].Name != "first" ||
		c.Entries[1].Name != "second" {
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
			assertTerminalSafeParseError(
				t,
				catalogWithPopular(t, tt.popular, validEntry(func(map[string]any) {})),
				tt.want,
			)
		})
	}
}

func catalogWithCategoryOrder(t *testing.T, order []string, entries ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schema_version": 1, "entries": entries, "category_order": order})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func categoryEntry(name, category string) map[string]any {
	return validEntry(func(e map[string]any) { e["name"], e["category"] = name, category })
}

func TestParseCategoryOrder_listedCategoriesComeFirstInListedOrder(t *testing.T) {
	entries := []map[string]any{
		categoryEntry("e1", "Alpha"), categoryEntry("e2", "Beta"), categoryEntry("e3", "Gamma"),
		categoryEntry("e4", "Alpha"), categoryEntry("e5", "Gamma"), categoryEntry("e6", "Delta"),
	}
	c, err := parse(catalogWithCategoryOrder(t, []string{"Gamma", "Alpha"}, entries...))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range c.Entries {
		names = append(names, entry.Name)
	}
	if want := []string{"e3", "e5", "e1", "e4", "e2", "e6"}; !slices.Equal(names, want) {
		t.Fatalf("entry order = %v, want %v", names, want)
	}
}

func TestParseCategoryOrder_unknownCategoryIsRefused(t *testing.T) {
	assertTerminalSafeParseError(
		t,
		catalogWithCategoryOrder(t, []string{"missing"}, validEntry(func(map[string]any) {})),
		`catalog category_order: "missing" is not a category of any server`,
	)
}

func TestParseCategoryOrder_repeatedCategoryIsRefused(t *testing.T) {
	assertTerminalSafeParseError(
		t,
		catalogWithCategoryOrder(t, []string{"c", "c"}, validEntry(func(map[string]any) {})),
		`catalog category_order: "c" is listed twice`,
	)
}

func TestOrderedByCategoryLeavesInputUnchanged(t *testing.T) {
	entries := []Entry{{Name: "a", Category: "B"}, {Name: "b", Category: "A"}}
	before := slices.Clone(entries)
	got := orderedByCategory(entries, []string{"A"})
	if !slices.Equal(entries, before) {
		t.Errorf("input changed to %+v", entries)
	}
	if got[0].Name != "b" || got[1].Name != "a" {
		t.Errorf("order = %+v, want b then a", got)
	}
}

package toon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixtureFile struct {
	Tests []fixtureCase `json:"tests"`
}

type fixtureCase struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Expected string          `json:"expected"`
	Options  fixtureOptions  `json:"options"`
}

type fixtureOptions struct {
	Delimiter    string `json:"delimiter"`
	Indent       *int   `json:"indent"`
	KeyFolding   string `json:"keyFolding"`
	FlattenDepth *int   `json:"flattenDepth"`
}

func (o fixtureOptions) matchesLockedConfig() bool {
	if o.Delimiter != "" && o.Delimiter != "," {
		return false
	}
	if o.Indent != nil && *o.Indent != 2 {
		return false
	}
	if o.KeyFolding != "" && o.KeyFolding != "safe" {
		return false
	}
	return o.FlattenDepth == nil
}

// fixtureSkips lists vendored spec fixtures that exercise behavior outside
// mini's locked encoder configuration (comma delimiter, 2-space indent, key
// folding always on with unlimited depth). Keys are "<file>/<test name>".
var fixtureSkips = map[string]string{
	"delimiters.json/encodes primitive arrays with tab delimiter":          "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes primitive arrays with pipe delimiter":         "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes tabular arrays with tab delimiter":            "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes tabular arrays with pipe delimiter":           "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes nested arrays with tab delimiter":             "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes nested arrays with pipe delimiter":            "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes root-level array with tab delimiter":          "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes root-level array with pipe delimiter":         "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes root-level array of objects with tab delimiter":  "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/encodes root-level array of objects with pipe delimiter": "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/quotes strings containing tab delimiter":              "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/quotes strings containing pipe delimiter":             "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/does not quote commas with tab delimiter":             "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/does not quote commas with pipe delimiter":            "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/does not quote commas in tabular values with tab delimiter": "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/does not quote commas in object values with pipe delimiter": "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/does not quote commas in object values with tab delimiter":  "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/quotes nested array values containing pipe delimiter": "pipe delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/quotes nested array values containing tab delimiter":  "tab delimiter option not implemented; delimiter fixed to comma",
	"delimiters.json/preserves ambiguity quoting regardless of delimiter":  "pipe delimiter option not implemented; delimiter fixed to comma",
	"whitespace.json/respects custom indent size option":                   "indent option not implemented; indent fixed to 2 spaces",
	"key-folding.json/encodes partial folding with flattenDepth=2":         "flattenDepth option not implemented; folding depth fixed to unlimited",
	"key-folding.json/encodes standard nesting with flattenDepth=0 (no folding)": "flattenDepth option not implemented; folding depth fixed to unlimited",
	"key-folding.json/encodes standard nesting with keyFolding=off (baseline)":   "keyFolding=off not implemented; folding is always on",
	"objects.json/encodes deeply nested objects":                           "fixture expects keyFolding=off default; folding is always on and folds a.b.c",
	"arrays-objects.json/uses list format for objects with nested values":  "fixture expects keyFolding=off default; folding is always on and folds nested.x",
	"arrays-objects.json/uses list format when one object has nested field": "fixture expects keyFolding=off default; folding is always on and folds data.nested",
}

func TestSpecEncodeFixtures(t *testing.T) {
	seen := make(map[string]bool, len(fixtureSkips))
	files, err := filepath.Glob(filepath.Join("testdata", "spec", "encode", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vendored fixture files found: %v", err)
	}
	for _, path := range files {
		runFixtureFile(t, path, seen)
	}
	for key := range fixtureSkips {
		if !seen[key] {
			t.Errorf("stale skip-list entry, no such fixture: %s", key)
		}
	}
}

func runFixtureFile(t *testing.T, path string, seenSkips map[string]bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var ff fixtureFile
	if err := json.Unmarshal(raw, &ff); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, tc := range ff.Tests {
		key := filepath.Base(path) + "/" + tc.Name
		t.Run(key, func(t *testing.T) {
			runFixtureCase(t, tc, key, seenSkips)
		})
	}
}

func runFixtureCase(t *testing.T, tc fixtureCase, key string, seenSkips map[string]bool) {
	if reason, ok := fixtureSkips[key]; ok {
		seenSkips[key] = true
		t.Skip(reason)
	}
	if !tc.Options.matchesLockedConfig() {
		t.Fatalf("fixture has options outside the locked config but is not on the skip list: %+v", tc.Options)
	}
	v, err := FromJSON(tc.Input)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != tc.Expected {
		t.Errorf("Encode() = %q, want %q", got, tc.Expected)
	}
}

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
	Delimiter string `json:"delimiter"`
	Indent    *int   `json:"indent"`
}

func (o fixtureOptions) matchesLockedConfig() bool {
	if o.Delimiter != "" && o.Delimiter != "," {
		return false
	}
	if o.Indent != nil && *o.Indent != 2 {
		return false
	}
	return true
}

// fixtureSkips lists vendored spec fixtures that exercise behavior outside
// mini's locked encoder configuration (comma delimiter, 2-space indent).
// Keys are "<file>/<test name>".
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
	"whitespace.json/respects custom indentSize option":                    "indent option not implemented; indent fixed to 2 spaces",

	"objects-keyed.json/encodes objects of uniform objects in keyed tabular form":         "keyed tabular form not yet implemented",
	"objects-keyed.json/encodes an eligible root object with a keyless keyed header":      "keyed tabular form not yet implemented",
	"objects-keyed.json/collapses uniform nested object columns inside keyed headers":     "keyed tabular form not yet implemented",
	"objects-keyed.json/orders fields by the first entry value's encounter order":         "keyed tabular form not yet implemented",
	"objects-keyed.json/uses the active delimiter in keyed headers and entry-row cells":   "keyed tabular form not yet implemented",
	"objects-keyed.json/quotes entry keys per key encoding":                               "keyed tabular form not yet implemented",
	"objects-keyed.json/quotes entry-row cells containing the active delimiter":           "keyed tabular form not yet implemented",
	"objects-keyed.json/keeps single-entry objects in nested form":                        "keyed tabular form not yet implemented",
	"objects-keyed.json/keeps objects in nested form when entry values have differing key sets": "keyed tabular form not yet implemented",
	"objects-keyed.json/keeps objects in nested form when a value is primitive":           "keyed tabular form not yet implemented",
	"objects-keyed.json/keeps objects in nested form when an entry value contains an array": "keyed tabular form not yet implemented",
	"objects-keyed.json/emits a keyed header on the hyphen line when it is the first field of a list item": "keyed tabular form not yet implemented",
	"objects-keyed.json/never encodes an anonymous array element in keyed tabular form":   "keyed tabular form not yet implemented",

	"arrays-tabular.json/collapses a uniform nested object column into a nested field group":  "nested field groups not yet implemented",
	"arrays-tabular.json/collapses sibling nested field groups with depth-first row layout":   "nested field groups not yet implemented",
	"arrays-tabular.json/collapses nested field groups recursively without a depth cap":       "nested field groups not yet implemented",
	"arrays-tabular.json/uses the active delimiter inside nested field groups":                "nested field groups not yet implemented",
	"arrays-tabular.json/quotes subfield names inside nested field groups per key encoding":   "nested field groups not yet implemented",

	"arrays-objects.json/encodes a keyed-eligible object in a tabular column as a nested field group": "nested field groups not yet implemented",
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

//go:build test

package response

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRaw_writesFile(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw([]byte(`{"raw":true,"items":["a","b"]}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	path := filepath.Join(s.dir, key+".json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("raw file not written: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !json.Valid(data) {
		t.Errorf("raw file is not valid JSON: %s", data)
	}
}

func TestWriteRaw_filenameIsMilliseconds(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw([]byte(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	base := key
	if idx := strings.IndexByte(key, '_'); idx > 0 {
		base = key[:idx]
	}
	if len(base) != 13 {
		t.Errorf("expected 13-digit unix ms key, got %s", key)
	}
}

func TestWriteRaw_unwritableDir(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: 0, BudgetMB: 100, CleanupInterval: 0})
	os.Chmod(dir, 0500) //nolint:errcheck
	defer os.Chmod(dir, 0700) //nolint:errcheck

	_, err := s.WriteRaw([]byte(`{"test":true}`))
	if err == nil {
		t.Error("expected error writing to unwritable dir")
	}
}

func TestWriteRaw_nonJSONPassesThrough(t *testing.T) {
	s := newStore(t)
	key, err := s.WriteRaw([]byte(`not json`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if string(data) != "not json" {
		t.Errorf("expected passthrough for non-JSON, got: %s", data)
	}
}

func TestWriteRaw_prettyPrintsValidJSON(t *testing.T) {
	s := newStore(t)
	compact := []byte(`{"a":1,"b":[1,2,3]}`)
	key, err := s.WriteRaw(compact)
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if string(data) == string(compact) {
		t.Error("expected pretty-printed output to differ from compact input")
	}
	if !json.Valid(data) {
		t.Errorf("expected valid JSON output, got: %s", data)
	}
}

func TestPrettyJSON_validJSON(t *testing.T) {
	compact := []byte(`{"a":1,"b":[1,2,3]}`)
	pretty := prettyJSON(compact)
	if !json.Valid(pretty) {
		t.Errorf("prettyJSON output is not valid JSON: %s", pretty)
	}
	if string(pretty) == string(compact) {
		t.Error("expected pretty-printed output to differ from compact")
	}
}

func TestPrettyJSON_invalidJSON_returnsOriginal(t *testing.T) {
	bad := []byte(`not json at all`)
	if string(prettyJSON(bad)) != string(bad) {
		t.Error("expected invalid JSON to pass through unchanged")
	}
}

func TestPrettyJSON_emptyObject(t *testing.T) {
	if !json.Valid(prettyJSON([]byte(`{}`))) {
		t.Error("expected valid JSON for empty object")
	}
}

func TestParseTimestamp_validName(t *testing.T) {
	cases := []struct {
		name     string
		wantYear int
	}{
		{"1750466675123.json", 2025},
		{"1750466675123_cafe.json", 2025},
	}
	for _, tc := range cases {
		ts, ok := parseTimestamp(tc.name)
		if !ok {
			t.Errorf("%s: expected valid timestamp, got false", tc.name)
			continue
		}
		if ts.Year() != tc.wantYear {
			t.Errorf("%s: got year %d, want %d", tc.name, ts.Year(), tc.wantYear)
		}
	}
}

func TestParseTimestamp_invalidNames(t *testing.T) {
	cases := []string{
		"short.json",
		"notafilename.json",
		"nodash.json",
		"nohash_.json",
		"abc_deadbeef.json",
		"1750466675123.raw.json",
	}
	for _, name := range cases {
		if _, ok := parseTimestamp(name); ok {
			t.Errorf("expected false for %s", name)
		}
	}
}

func TestPrettyJSON_PreservesLargeIntegers(t *testing.T) {
	input := []byte(`{"id":9007199254740993,"name":"test"}`)
	got := prettyJSON(input)
	if !strings.Contains(string(got), "9007199254740993") {
		t.Errorf("prettyJSON corrupted large integer: %s", got)
	}
}

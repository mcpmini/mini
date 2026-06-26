//go:build test

package response

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func assertStoreEmpty(t *testing.T, s *Store, path string) {
	t.Helper()
	count, used := s.Stats()
	if count != 0 {
		t.Errorf("expected 0 files after eviction, got %d", count)
	}
	if used != 0 {
		t.Errorf("expected 0 bytes after eviction, got %d", used)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted after TTL expiry")
	}
}


func TestEvictExpired_keepsNonExpired(t *testing.T) {
	s := newStore(t)

	if _, err := s.WriteRaw([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteRaw([]byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}

	count, _ := s.Stats()
	if count != 2 {
		t.Fatalf("expected 2 files, got %d", count)
	}

	s.evictExpired() // TTL is 1 hour — nothing should be evicted

	count, _ = s.Stats()
	if count != 2 {
		t.Errorf("expected 2 files after non-expiring eviction, got %d", count)
	}
}



func TestLoadExisting_picksUpFilesFromPreviousSession(t *testing.T) {
	dir := t.TempDir()

	// Write a file directly using one Store instance
	s1, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	s1.WriteRaw([]byte(`{"session":1}`))
	s1.WriteRaw([]byte(`{"session":2}`))
	s1.Close()

	// New Store instance should pick up the existing files
	s2, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s2.Close()

	count, _ := s2.Stats()
	if count != 2 {
		t.Errorf("expected 2 files loaded from previous session, got %d", count)
	}
}


func TestLoadExisting_ignoresNonTimestampFiles(t *testing.T) {
	dir := t.TempDir()
	// Write a file with a non-timestamp name
	os.WriteFile(filepath.Join(dir, "not-a-timestamp.json"), []byte(`{}`), 0600)

	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s.Close()

	count, _ := s.Stats()
	if count != 0 {
		t.Errorf("expected non-timestamp files to be ignored, got %d", count)
	}
}

func TestEvictOvershoot_concurrentWritesBudgetEnforced(t *testing.T) {
	dir := t.TempDir()
	// Tiny budget: 2 files of ~600KB each = 1.2MB > 1MB budget.
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	chunk := []byte(`{"data":"` + strings.Repeat("x", 600*1024) + `"}`)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.WriteRaw(chunk) //nolint:errcheck
		}()
	}
	wg.Wait()

	_, usedBytes := s.Stats()
	budgetBytes := int64(1 * 1024 * 1024)
	if usedBytes > budgetBytes {
		t.Errorf("budget exceeded after concurrent writes: used %d > budget %d", usedBytes, budgetBytes)
	}
}

func TestEvictOvershoot_keepsNewestFile(t *testing.T) {
	dir := t.TempDir()
	// Budget smaller than a single file — the just-written file must survive.
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	// Write an initial small file that fits.
	small := []byte(`{"data":"small"}`)
	path1, _ := s.WriteRaw(small)

	// Write a file that together with path1 exceeds budget.
	large := []byte(`{"data":"` + strings.Repeat("x", 900*1024) + `"}`)
	path2, err := s.WriteRaw(large)
	if err != nil {
		t.Fatalf("WriteRaw large: %v", err)
	}

	if _, err := os.Stat(path2); err != nil {
		t.Error("newest (large) file should be kept even if it exceeds budget alone")
	}
	_ = path1
}

func TestEvictIfNeeded_removesOldestWhenOverBudget(t *testing.T) {
	dir := t.TempDir()
	// 1 MB budget — large enough to hold first file, but tight enough that
	// writing many files eventually triggers eviction. We use a tiny budget
	// by writing files large enough to overflow 1 MB.
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	large := []byte(`{"data":"` + strings.Repeat("x", 600*1024) + `"}`)
	path1, _ := s.WriteRaw(large)
	path2, _ := s.WriteRaw(large) // second write should evict path1

	_ = path2
	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Error("expected oldest file to be evicted when over budget")
	}
}

func TestWriteRaw_unwritableDir(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s.Close()

	// Make dir unwritable
	os.Chmod(dir, 0500)
	defer os.Chmod(dir, 0700)

	_, err := s.WriteRaw([]byte(`{"test":true}`))
	if err == nil {
		t.Error("expected error writing to unwritable dir")
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
	out := prettyJSON(bad)
	if string(out) != string(bad) {
		t.Errorf("expected invalid JSON to pass through unchanged, got: %s", out)
	}
}

func TestPrettyJSON_emptyObject(t *testing.T) {
	out := prettyJSON([]byte(`{}`))
	if !json.Valid(out) {
		t.Errorf("expected valid JSON, got: %s", out)
	}
}

func TestParseTimestamp_validName(t *testing.T) {
	name := "1750466675_c0ffee00.json"
	ts, ok := parseTimestamp(name)
	if !ok {
		t.Fatalf("expected valid timestamp, got false")
	}
	if ts.Year() != 2025 {
		t.Errorf("unexpected year for epoch 1750466675: %v", ts)
	}
}

func TestParseTimestamp_tooShort(t *testing.T) {
	_, ok := parseTimestamp("short.json")
	if ok {
		t.Error("expected false for too-short name")
	}
}

func TestParseTimestamp_invalidFormat(t *testing.T) {
	cases := []string{
		"notafilename.json",
		"nodash.json",
		"nohash_.json",
		"abc_deadbeef.json",
	}
	for _, name := range cases {
		_, ok := parseTimestamp(name)
		if ok {
			t.Errorf("expected false for %s", name)
		}
	}
}

func TestParseTimestamp_rawFileIgnored(t *testing.T) {
	name := "1750466675_deadbeef.raw.json"
	_, ok := parseTimestamp(name)
	if !ok {
		t.Error("parseTimestamp doesn't filter raw files (loadExisting does) — this should parse ok")
	}
}

func TestWriteRaw_writesFile(t *testing.T) {
	s := newStore(t)
	path, err := s.WriteRaw([]byte(`{"raw":true,"items":["a","b"]}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("raw file not written: %v", err)
	}

	rawData, _ := os.ReadFile(path)
	if !json.Valid(rawData) {
		t.Errorf("raw file is not valid JSON: %s", rawData)
	}
}

func TestWriteRaw_filenameHasHashSuffix(t *testing.T) {
	s := newStore(t)
	path, err := s.WriteRaw([]byte(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 || len(parts[0]) < 10 || len(parts[1]) != 8 {
		t.Errorf("expected {epoch}_{hash8}.json, got %s", filepath.Base(path))
	}
}

func TestNewStore_invalidDir(t *testing.T) {
	// Try to create a store inside a file (not a directory)
	f, _ := os.CreateTemp(t.TempDir(), "file")
	f.Close()
	_, err := NewStore(StoreConfig{Dir: f.Name() + "/subdir", TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	if err == nil {
		t.Error("expected error when creating store in invalid path")
	}
}

func TestWriteRaw_nonJSONResponsesFromUpstreamsWrittenSuccessfully(t *testing.T) {
	s := newStore(t)
	path, err := s.WriteRaw([]byte(`not json`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "not json" {
		t.Errorf("expected passthrough for non-JSON, got: %s", data)
	}
}

func concurrentWriteRaw(s *Store, n int) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = s.WriteRaw([]byte(fmt.Sprintf(`{"i":%d}`, idx)))
		}(i)
	}
	wg.Wait()
	return errs
}

func TestWriteRaw_concurrent(t *testing.T) {
	s := newStore(t)
	const n = 50
	for i, err := range concurrentWriteRaw(s, n) {
		if err != nil {
			t.Errorf("goroutine %d: WriteRaw error: %v", i, err)
		}
	}
	count, _ := s.Stats()
	if count != n {
		t.Errorf("expected %d files after concurrent writes, got %d", n, count)
	}
}

func TestWriteRaw_prettyPrintsValidJSON(t *testing.T) {
	s := newStore(t)
	compact := []byte(`{"a":1,"b":[1,2,3]}`)
	path, err := s.WriteRaw(compact)
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) == string(compact) {
		t.Error("expected pretty-printed output to differ from compact input")
	}
	if !json.Valid(data) {
		t.Errorf("expected valid JSON output, got: %s", data)
	}
}

func TestEvictIfNeeded_zeroBudget_unlimited(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 0, CleanupInterval: time.Hour}) // 0 MB = unlimited
	defer s.Close()

	for i := 0; i < 3; i++ {
		if _, err := s.WriteRaw([]byte(`{"n":1}`)); err != nil {
			t.Fatalf("WriteRaw %d: %v", i, err)
		}
	}

	count, _ := s.Stats()
	if count != 3 {
		t.Errorf("expected all 3 files retained (unlimited budget), got %d", count)
	}
}

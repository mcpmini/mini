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

func TestEvictExpired_removesExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: 50 * time.Millisecond, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s.Close()
	path, err := s.WriteRaw([]byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	count, _ := s.Stats()
	if count != 1 {
		t.Fatalf("expected 1 file before eviction, got %d", count)
	}
	time.Sleep(100 * time.Millisecond)
	s.evictExpired()
	assertStoreEmpty(t, s, path)
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

// TestEvictExpired_removesLegacyRawPairFile covers the migration case: a
// .raw.json companion left over from an older mini version that wrote
// slim/raw pairs must still be cleaned up via storedFile.rawPath.
func TestEvictExpired_removesLegacyRawPairFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s.Close()

	path := filepath.Join(dir, "20260101000000000.json")
	rawPath := filepath.Join(dir, "20260101000000000.raw.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rawPath, []byte(`{"full":"data"}`), 0600); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.files = append(s.files, storedFile{path: path, rawPath: rawPath, size: 1, expires: time.Now().Add(-time.Minute)})
	s.mu.Unlock()
	s.evictExpired()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("slim file should be deleted after TTL")
	}
	if _, err := os.Stat(rawPath); !os.IsNotExist(err) {
		t.Error("raw file should be deleted after TTL")
	}
}

func TestCleanupLoop_evictsExpiredFilesAutomatically(t *testing.T) {
	dir := t.TempDir()
	// Short TTL + short cleanup interval
	s, err := NewStore(StoreConfig{Dir: dir, TTL: 50*time.Millisecond, BudgetMB: 100, CleanupInterval: 30*time.Millisecond})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	path, _ := s.WriteRaw([]byte(`{"test":true}`))

	// Wait for the cleanup loop to fire and evict the expired file
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return // evicted — test passes
		}
	}
	t.Error("cleanup loop did not evict expired file within 2 seconds")
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

func TestLoadExisting_evictsExpiredFromPreviousSession(t *testing.T) {
	dir := t.TempDir()
	ttl := 50 * time.Millisecond

	s1, _ := NewStore(StoreConfig{Dir: dir, TTL: ttl, BudgetMB: 100, CleanupInterval: time.Hour})
	s1.WriteRaw([]byte(`{"old":true}`))
	s1.Close()

	// Wait longer than TTL — file should be considered expired by new Store
	time.Sleep(100 * time.Millisecond)

	// New Store with same TTL: created_at + TTL < now → file is expired on load
	s2, _ := NewStore(StoreConfig{Dir: dir, TTL: ttl, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s2.Close()

	count, _ := s2.Stats()
	if count != 0 {
		t.Errorf("expected 0 files (expired on load), got %d", count)
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
	// Concurrent writes both pass evictIfNeeded, then evictOvershoot cleans up.
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
	// evictIfNeeded removes path1 first, then writes large. Verify large is kept.
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
	// tsLayout = "20060102150405" (14 chars) + 3 ms digits = 17 chars
	name := "20260314123456789.json"
	ts, ok := parseTimestamp(name)
	if !ok {
		t.Fatalf("expected valid timestamp, got false")
	}
	if ts.Year() != 2026 || ts.Month() != 3 || ts.Day() != 14 {
		t.Errorf("unexpected parsed time: %v", ts)
	}
}

func TestParseTimestamp_tooShort(t *testing.T) {
	_, ok := parseTimestamp("short.json")
	if ok {
		t.Error("expected false for too-short name")
	}
}

func TestParseTimestamp_invalidFormat(t *testing.T) {
	// 17+ chars but not a valid timestamp
	_, ok := parseTimestamp("notavalidtimesta.json")
	if ok {
		t.Error("expected false for invalid timestamp format")
	}
}

func TestParseTimestamp_rawFileIgnored(t *testing.T) {
	// raw files have ".raw.json" extension, loadExisting skips them
	// This tests that the ext check works (raw files contain ".raw.")
	name := "20260314123456789.raw.json"
	// This has ext ".json" technically but contains ".raw." so loadExisting skips it
	// parseTimestamp itself would parse it, the filtering is in loadExisting
	_, ok := parseTimestamp(name)
	if !ok {
		t.Error("parseTimestamp doesn't filter raw files (loadExisting does) — this should parse ok")
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

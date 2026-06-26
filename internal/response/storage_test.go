//go:build test

package response

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

var clockTestBase = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

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

func storeWithClock(t *testing.T, clk clock.Clock) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour, Clock: clk})
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

func epochBase(at time.Time) string {
	return fmt.Sprintf("%d", at.UnixMilli())
}

func TestNewStore_invalidDir(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "file")
	f.Close()
	_, err := NewStore(StoreConfig{Dir: f.Name() + "/subdir", TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	if err == nil {
		t.Error("expected error when creating store in invalid path")
	}
}

func TestEvictExpired_keepsNonExpired(t *testing.T) {
	s := newStore(t)
	s.WriteRaw([]byte(`{"a":1}`)) //nolint:errcheck
	s.WriteRaw([]byte(`{"b":2}`)) //nolint:errcheck

	s.evictExpired()

	count, _ := s.Stats()
	if count != 2 {
		t.Errorf("expected 2 files after non-expiring eviction, got %d", count)
	}
}

func TestEvictExpired_removesExpiredFiles(t *testing.T) {
	clk := clock.NewFake(clockTestBase)
	s := storeWithClock(t, clk)

	path, err := s.WriteRaw([]byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	clk.Advance(2 * time.Hour)
	s.evictExpired()
	assertStoreEmpty(t, s, path)
}

func TestEvictExpired_deletesFileFromDisk(t *testing.T) {
	clk := clock.NewFake(clockTestBase)
	s := storeWithClock(t, clk)

	path, err := s.WriteRaw([]byte(`{"full":"data"}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	clk.Advance(2 * time.Hour)
	s.evictExpired()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("raw file should be deleted after TTL")
	}
}

func TestCleanupLoop_evictsExpiredFilesAutomatically(t *testing.T) {
	clk := clock.NewFake(clockTestBase)
	dir := t.TempDir()
	s, err := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour, Clock: clk})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	path, err := s.WriteRaw([]byte(`{"test":true}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("cleanupLoop timer not registered: %v", err)
	}
	clk.Advance(2 * time.Hour)
	// Re-registration of the next timer proves evictExpired already ran.
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("cleanupLoop did not re-register after eviction: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup loop did not evict expired file")
	}
}

func TestLoadExisting_picksUpFilesFromPreviousSession(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	s1.WriteRaw([]byte(`{"session":1}`)) //nolint:errcheck
	s1.WriteRaw([]byte(`{"session":2}`)) //nolint:errcheck
	s1.Close()

	s2, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s2.Close()

	count, _ := s2.Stats()
	if count != 2 {
		t.Errorf("expected 2 files loaded from previous session, got %d", count)
	}
}

func TestLoadExisting_evictsExpiredFromPreviousSession(t *testing.T) {
	dir := t.TempDir()
	clk1 := clock.NewFake(clockTestBase)
	s1, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour, Clock: clk1})
	s1.WriteRaw([]byte(`{"old":true}`)) //nolint:errcheck
	s1.Close()

	// clk2 is 2h ahead: file expires at clockTestBase+1h, now=clockTestBase+2h → expired on load.
	clk2 := clock.NewFake(clockTestBase.Add(2 * time.Hour))
	s2, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour, Clock: clk2})
	defer s2.Close()

	count, _ := s2.Stats()
	if count != 0 {
		t.Errorf("expected 0 files (expired on load), got %d", count)
	}
}

func TestLoadExisting_ignoresNonTimestampFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "not-a-timestamp.json"), []byte(`{}`), 0600) //nolint:errcheck
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
	defer s.Close()

	count, _ := s.Stats()
	if count != 0 {
		t.Errorf("expected non-timestamp files to be ignored, got %d", count)
	}
}

func TestLoadEntry_recordsSize(t *testing.T) {
	dir := t.TempDir()
	slimPath := filepath.Join(dir, epochBase(time.Now())+".json")
	data := []byte(`{"ok":true}`)
	if err := os.WriteFile(slimPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, ttl: time.Hour}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == filepath.Base(slimPath) {
			s.loadEntry(e, time.Now())
		}
	}
	if len(s.files) != 1 {
		t.Fatalf("expected 1 loaded file, got %d", len(s.files))
	}
	if s.usedBytes != int64(len(data)) {
		t.Fatalf("usedBytes = %d, want %d", s.usedBytes, len(data))
	}
}

func TestLoadEntry_skipsRawCompanionFiles(t *testing.T) {
	dir := t.TempDir()
	rawPath := filepath.Join(dir, epochBase(time.Now())+".raw.json")
	if err := os.WriteFile(rawPath, []byte(`{"full":"data"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, ttl: time.Hour}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		s.loadEntry(entry, time.Now())
	}
	if len(s.files) != 0 {
		t.Fatalf("expected raw companion file to be skipped, got %d entries", len(s.files))
	}
}

func TestEvictOvershoot_concurrentWritesBudgetEnforced(t *testing.T) {
	dir := t.TempDir()
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
	if usedBytes > int64(1*1024*1024) {
		t.Errorf("budget exceeded after concurrent writes: used %d > budget %d", usedBytes, 1*1024*1024)
	}
}

func TestEvictOvershoot_keepsNewestFile(t *testing.T) {
	dir := t.TempDir()
	// Budget smaller than a single large file — the just-written file must survive.
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	path1, _ := s.WriteRaw([]byte(`{"data":"small"}`))
	path2, err := s.WriteRaw([]byte(`{"data":"` + strings.Repeat("x", 900*1024) + `"}`))
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
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	large := []byte(`{"data":"` + strings.Repeat("x", 600*1024) + `"}`)
	path1, _ := s.WriteRaw(large)
	s.WriteRaw(large) //nolint:errcheck

	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Error("expected oldest file to be evicted when over budget")
	}
}

func TestEvictIfNeeded_zeroBudget_unlimited(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 0, CleanupInterval: time.Hour})
	defer s.Close()

	for range 3 {
		if _, err := s.WriteRaw([]byte(`{"n":1}`)); err != nil {
			t.Fatalf("WriteRaw: %v", err)
		}
	}
	count, _ := s.Stats()
	if count != 3 {
		t.Errorf("expected all 3 files retained (unlimited budget), got %d", count)
	}
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

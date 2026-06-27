//go:build test

package response

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func newTestStore(t *testing.T, cfg StoreConfig) *Store {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.NewFake()
	}
	s, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return newTestStore(t, StoreConfig{TTL: time.Hour, BudgetMB: 100, CleanupInterval: time.Hour})
}

func assertStoreEmpty(t *testing.T, s *Store, key string) {
	t.Helper()
	count, used := s.Stats()
	if count != 0 {
		t.Errorf("expected 0 files after eviction, got %d", count)
	}
	if used != 0 {
		t.Errorf("expected 0 bytes after eviction, got %d", used)
	}
	if _, err := os.Stat(filepath.Join(s.dir, key+".json")); !os.IsNotExist(err) {
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
	fakeClock := clock.NewFake()
	dir := t.TempDir()
	s := newTestStore(t, StoreConfig{Dir: dir, TTL: time.Minute, BudgetMB: 100, CleanupInterval: time.Hour, Clock: fakeClock})

	key, err := s.WriteRaw([]byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	fakeClock.Advance(2 * time.Minute)
	s.evictExpired()
	assertStoreEmpty(t, s, key)
}

func TestEvictExpired_removesRawFile(t *testing.T) {
	fakeClock := clock.NewFake()
	dir := t.TempDir()
	s := newTestStore(t, StoreConfig{Dir: dir, TTL: time.Minute, BudgetMB: 100, CleanupInterval: time.Hour, Clock: fakeClock})

	key, err := s.WriteRaw([]byte(`{"full":"data"}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	fakeClock.Advance(2 * time.Minute)
	s.evictExpired()

	if _, err := os.Stat(filepath.Join(s.dir, key+".json")); !os.IsNotExist(err) {
		t.Error("raw file should be deleted after TTL")
	}
}

func TestCleanupLoop_evictsExpiredFilesAutomatically(t *testing.T) {
	fakeClock := clock.NewFake()
	dir := t.TempDir()
	evicted := make(chan struct{}, 1)
	s := newTestStore(t, StoreConfig{Dir: dir, TTL: time.Minute, BudgetMB: 100, CleanupInterval: 5 * time.Minute, Clock: fakeClock, AfterEvict: func() { evicted <- struct{}{} }})

	key, _ := s.WriteRaw([]byte(`{"test":true}`))

	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatalf("waiting for cleanup timer: %v", err)
	}
	fakeClock.Advance(time.Minute + 5*time.Minute)

	select {
	case <-evicted:
	case <-t.Context().Done():
		t.Fatal("cleanup loop did not run")
	}
	if _, err := os.Stat(filepath.Join(s.dir, key+".json")); !os.IsNotExist(err) {
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
	fc := clock.NewFake()
	dir := t.TempDir()

	s1 := newTestStore(t, StoreConfig{Dir: dir, TTL: time.Minute, BudgetMB: 100, CleanupInterval: time.Hour, Clock: fc})
	s1.WriteRaw([]byte(`{"old":true}`)) //nolint:errcheck
	s1.Close()

	fc.Advance(2 * time.Minute)
	s2 := newTestStore(t, StoreConfig{Dir: dir, TTL: time.Minute, BudgetMB: 100, CleanupInterval: time.Hour, Clock: fc})
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
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer s.Close()

	key1, _ := s.WriteRaw([]byte(`{"data":"small"}`))
	key2, err := s.WriteRaw([]byte(`{"data":"` + strings.Repeat("x", 900*1024) + `"}`))
	if err != nil {
		t.Fatalf("WriteRaw large: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, key2+".json")); err != nil {
		t.Error("newest (large) file should be kept even if it exceeds budget alone")
	}
	_ = key1
}

func TestEvictIfNeeded_removesOldestWhenOverBudget(t *testing.T) {
	fakeClock := clock.NewFake()
	dir := t.TempDir()
	s, _ := NewStore(StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour, Clock: fakeClock})
	defer s.Close()

	large := []byte(`{"data":"` + strings.Repeat("x", 600*1024) + `"}`)
	key1, _ := s.WriteRaw(large)
	fakeClock.Advance(time.Millisecond)
	s.WriteRaw(large) //nolint:errcheck

	if _, err := os.Stat(filepath.Join(s.dir, key1+".json")); !os.IsNotExist(err) {
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

//go:build test

package response

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

var clockTestBase = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

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

func TestEvictExpired_removesExpiredFiles(t *testing.T) {
	clk := clock.NewFake(clockTestBase)
	s := storeWithClock(t, clk)

	path, err := s.WriteRaw([]byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	count, _ := s.Stats()
	if count != 1 {
		t.Fatalf("expected 1 file before eviction, got %d", count)
	}

	clk.Advance(2 * time.Hour)
	s.evictExpired()
	assertStoreEmpty(t, s, path)
}

func TestEvictExpired_removesRawFile(t *testing.T) {
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
	// Wait for the loop to re-register its next timer, proving evictExpired already ran.
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("cleanupLoop did not re-register after eviction: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup loop did not evict expired file")
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

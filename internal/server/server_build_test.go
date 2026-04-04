package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/response"
)

func TestBuildStoreConfig(t *testing.T) {
	t.Run("uses defaults when values are missing or invalid", testBuildStoreConfigDefaults)
	t.Run("preserves explicit values", testBuildStoreConfigExplicit)
}

func testBuildStoreConfigDefaults(t *testing.T) {
	t.Helper()
	cfg := &config.Config{
		ResponseDiskBudgetMB:    7,
		ResponseTTL:             "bad",
		ResponseCleanupInterval: "bad",
	}
	got := buildStoreConfig(cfg)
	home, _ := os.UserHomeDir()
	assertStoreConfigBasics(t, got, filepath.Join(home, ".mini", "responses"), 168*time.Hour, time.Hour, 7)
}

func testBuildStoreConfigExplicit(t *testing.T) {
	t.Helper()
	cfg := &config.Config{
		ResponseDir:             t.TempDir(),
		ResponseTTL:             "30m",
		ResponseCleanupInterval: "15s",
		ResponseDiskBudgetMB:    42,
	}
	got := buildStoreConfig(cfg)
	assertStoreConfigBasics(t, got, cfg.ResponseDir, 30*time.Minute, 15*time.Second, 42)
}

func assertStoreConfigBasics(t *testing.T, got response.StoreConfig, wantDir string, wantTTL, wantCleanup time.Duration, wantBudget int) {
	t.Helper()
	if got.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", got.Dir, wantDir)
	}
	if got.TTL != wantTTL {
		t.Fatalf("TTL = %v, want %v", got.TTL, wantTTL)
	}
	if got.CleanupInterval != wantCleanup {
		t.Fatalf("CleanupInterval = %v, want %v", got.CleanupInterval, wantCleanup)
	}
	if got.BudgetMB != wantBudget {
		t.Fatalf("BudgetMB = %d, want %d", got.BudgetMB, wantBudget)
	}
}

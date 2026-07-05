//go:build test

package forge_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/forge"
)

func TestSweepStaleDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	mkDir := func(name string, age time.Duration) string {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		mtime := time.Now().Add(-age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
		return path
	}

	staleDeno := mkDir("forge-deno-stale", 8*24*time.Hour)
	freshDeno := mkDir("forge-deno-fresh", 2*24*time.Hour)
	staleScratch := mkDir("forge-scratch-stale", 48*time.Hour)
	freshScratch := mkDir("forge-scratch-fresh", 1*time.Hour)
	other := mkDir("other-stale", 8*24*time.Hour)

	forge.SweepStaleDirs(slog.New(slog.NewTextHandler(io.Discard, nil)))

	assertRemoved := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", filepath.Base(path))
		}
	}
	assertExists := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", filepath.Base(path), err)
		}
	}

	assertRemoved(staleDeno)
	assertExists(freshDeno)
	assertRemoved(staleScratch)
	assertExists(freshScratch)
	assertExists(other)
}

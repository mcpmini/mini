package ops_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/ops"
)

func TestPurgeExpiredResponses(t *testing.T) {
	t.Run("returns zero when no responses directory exists", func(t *testing.T) {
		dir := tempDir(t)
		fakeClock := clock.NewFake()
		removed, freed, err := ops.PurgeExpiredResponses(dir, fakeClock.Now())
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 || freed != 0 {
			t.Errorf("got removed=%d freed=%d, want 0, 0", removed, freed)
		}
	})

	t.Run("removes expired json file and reports exact bytes freed", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "internal", "responses")
		os.MkdirAll(respDir, 0700)

		jsonBody := []byte(`{"ok":true}`)
		oldJSON := filepath.Join(respDir, "old.json")
		os.WriteFile(oldJSON, jsonBody, 0600)
		fakeClock := clock.NewFake()
		past := fakeClock.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)

		removed, freed, err := ops.PurgeExpiredResponses(dir, fakeClock.Now())
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		if freed != int64(len(jsonBody)) {
			t.Errorf("freed = %d, want %d", freed, len(jsonBody))
		}
		if _, err := os.Stat(oldJSON); err == nil {
			t.Error("old.json still exists")
		}
	})

	t.Run("does not remove fresh files", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "internal", "responses")
		os.MkdirAll(respDir, 0700)
		freshJSON := filepath.Join(respDir, "fresh.json")
		os.WriteFile(freshJSON, []byte(`{"ok":true}`), 0600)

		fakeClock := clock.NewFake()
		removed, _, err := ops.PurgeExpiredResponses(dir, fakeClock.Now())
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 {
			t.Errorf("removed = %d, want 0", removed)
		}
		if _, err := os.Stat(freshJSON); err != nil {
			t.Error("fresh.json was incorrectly removed")
		}
	})

	t.Run("removes expired json when raw counterpart is absent", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "internal", "responses")
		os.MkdirAll(respDir, 0700)
		jsonBody := []byte(`{"ok":true}`)
		oldJSON := filepath.Join(respDir, "solo.json")
		os.WriteFile(oldJSON, jsonBody, 0600)
		fakeClock := clock.NewFake()
		past := fakeClock.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)

		removed, freed, err := ops.PurgeExpiredResponses(dir, fakeClock.Now())
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		if freed != int64(len(jsonBody)) {
			t.Errorf("freed = %d, want %d", freed, len(jsonBody))
		}
	})

	t.Run("legacy raw.json orphan files are cleaned up", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "internal", "responses")
		os.MkdirAll(respDir, 0700)
		rawOnly := filepath.Join(respDir, "orphan.raw.json")
		os.WriteFile(rawOnly, []byte(`{}`), 0600)
		fakeClock := clock.NewFake()
		past := fakeClock.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(rawOnly, past, past)

		removed, _, err := ops.PurgeExpiredResponses(dir, fakeClock.Now())
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1 (legacy .raw.json files should be cleaned up)", removed)
		}
		if _, err := os.Stat(rawOnly); err == nil {
			t.Error("orphan.raw.json should have been deleted")
		}
	})
}

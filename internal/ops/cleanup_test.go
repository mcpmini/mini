package ops_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/ops"
)

func TestPurgeExpiredResponses(t *testing.T) {
	t.Run("returns zero when no responses directory exists", func(t *testing.T) {
		dir := tempDir(t)
		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 || freed != 0 {
			t.Errorf("got removed=%d freed=%d, want 0, 0", removed, freed)
		}
	})

	t.Run("removes expired json+raw pair and reports exact bytes freed", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)

		jsonBody := []byte(`{"ok":true}`)
		rawBody := []byte(`{"raw":true,"extra":"data"}`)
		oldJSON := filepath.Join(respDir, "old.json")
		oldRaw := filepath.Join(respDir, "old.raw.json")
		os.WriteFile(oldJSON, jsonBody, 0600)
		os.WriteFile(oldRaw, rawBody, 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)
		os.Chtimes(oldRaw, past, past)

		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		wantFreed := int64(len(jsonBody) + len(rawBody))
		if freed != wantFreed {
			t.Errorf("freed = %d, want %d", freed, wantFreed)
		}
		if _, err := os.Stat(oldJSON); err == nil {
			t.Error("old.json still exists")
		}
		if _, err := os.Stat(oldRaw); err == nil {
			t.Error("old.raw.json still exists")
		}
	})

	t.Run("does not remove fresh files", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)
		freshJSON := filepath.Join(respDir, "fresh.json")
		os.WriteFile(freshJSON, []byte(`{"ok":true}`), 0600)

		removed, _, err := ops.PurgeExpiredResponses(dir)
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
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)
		jsonBody := []byte(`{"ok":true}`)
		oldJSON := filepath.Join(respDir, "solo.json")
		os.WriteFile(oldJSON, jsonBody, 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)

		removed, freed, err := ops.PurgeExpiredResponses(dir)
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

	t.Run("raw.json files are skipped as primary entries", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)
		rawOnly := filepath.Join(respDir, "orphan.raw.json")
		os.WriteFile(rawOnly, []byte(`{}`), 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(rawOnly, past, past)

		removed, _, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 {
			t.Errorf("removed = %d, want 0 (raw.json should not be counted as primary)", removed)
		}
	})
}

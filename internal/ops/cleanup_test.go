
package ops_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/ops"
)

func TestPurgeExpiredResponses(t *testing.T) {
	t.Run("no responses dir returns zero", func(t *testing.T) {
		dir := tempDir(t)
		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 || freed != 0 {
			t.Errorf("expected 0, 0; got %d, %d", removed, freed)
		}
	})

	t.Run("removes expired pairs", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)

		oldJSON := filepath.Join(respDir, "old.json")
		oldRaw := filepath.Join(respDir, "old.raw.json")
		os.WriteFile(oldJSON, []byte(`{"ok":true}`), 0600)
		os.WriteFile(oldRaw, []byte(`{"raw":true}`), 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)
		os.Chtimes(oldRaw, past, past)

		freshJSON := filepath.Join(respDir, "fresh.json")
		os.WriteFile(freshJSON, []byte(`{"ok":true}`), 0600)

		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		if freed == 0 {
			t.Error("freed should be > 0")
		}
		if _, err := os.Stat(oldJSON); err == nil {
			t.Error("old.json still exists")
		}
		if _, err := os.Stat(freshJSON); err != nil {
			t.Error("fresh.json was removed")
		}
	})
}

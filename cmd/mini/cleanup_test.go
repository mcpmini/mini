package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCleanup(t *testing.T) {
	t.Run("no responses directory prints nothing to clean up", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runCleanup(dir, &out); err != nil {
			t.Fatalf("runCleanup: %v", err)
		}
		if !strings.Contains(out.String(), "nothing to clean up") {
			t.Errorf("output = %q, want 'nothing to clean up'", out.String())
		}
	})

	t.Run("reports removed count and freed bytes for expired files", func(t *testing.T) {
		dir := t.TempDir()
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)

		oldJSON := filepath.Join(respDir, "old.json")
		oldRaw := filepath.Join(respDir, "old.raw.json")
		os.WriteFile(oldJSON, []byte(`{"ok":true}`), 0600)
		os.WriteFile(oldRaw, []byte(`{"raw":true}`), 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)
		os.Chtimes(oldRaw, past, past)

		var out bytes.Buffer
		if err := runCleanup(dir, &out); err != nil {
			t.Fatalf("runCleanup: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "removed 1 file pair(s)") {
			t.Errorf("output = %q, want 'removed 1 file pair(s)'", got)
		}
		if !strings.Contains(got, "freed") {
			t.Errorf("output = %q, want 'freed' bytes info", got)
		}
	})

	t.Run("does not remove fresh files", func(t *testing.T) {
		dir := t.TempDir()
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)
		freshJSON := filepath.Join(respDir, "fresh.json")
		os.WriteFile(freshJSON, []byte(`{"ok":true}`), 0600)

		var out bytes.Buffer
		runCleanup(dir, &out) //nolint:errcheck

		if _, err := os.Stat(freshJSON); err != nil {
			t.Error("fresh.json was incorrectly removed")
		}
		if !strings.Contains(out.String(), "nothing to clean up") {
			t.Errorf("output = %q, want 'nothing to clean up'", out.String())
		}
	})
}

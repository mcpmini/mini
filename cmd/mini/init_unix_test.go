//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestInitFromAPipeImportsItsServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	pipe := filepath.Join(t.TempDir(), "team.json")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		// The pipe holds the config for one reader only, as --from <(...) does.
		w, err := os.OpenFile(pipe, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w.WriteString(
			`{"mcpServers":{"search":{"url":"https://search.example/mcp"}}}`,
		) //nolint:errcheck // a short write fails the assertion below
		w.Close() //nolint:errcheck // nothing to flush on a pipe
	}()
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--from", pipe})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := os.Stat(filepath.Join(configDir, "servers", "search.yaml")); err != nil {
		t.Errorf("search.yaml not written: %v\n%s", err, out)
	}
}

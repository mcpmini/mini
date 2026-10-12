//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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
	// One writer, so the config can be read once, as from --from <(...).
	go func() {
		config := []byte(`{"mcpServers":{"search":{"url":"https://search.example/mcp"}}}`)
		// testutil.WriteFile fails with t.Fatal, which a goroutine can't call.
		if err := os.WriteFile(pipe, config, 0o600); err != nil { //fileiolint:allow see above
			t.Error(err)
		}
	}()
	// A second read waits for a writer that never comes; an empty one ends it, so a regression fails
	// the test instead of hanging it.
	unblock := time.AfterFunc(5*time.Second, func() { closeAnEnd(pipe, os.O_WRONLY) })
	t.Cleanup(func() {
		unblock.Stop()
		closeAnEnd(pipe, os.O_RDONLY) // releases the writer if init never read
	})
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--from", pipe})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Error(err)
		}
	})

	if _, err := os.Stat(filepath.Join(configDir, "servers", "search.yaml")); err != nil {
		t.Errorf("search.yaml not written: %v\n%s", err, out)
	}
}

func closeAnEnd(pipe string, flag int) {
	f, err := os.OpenFile(pipe, flag|syscall.O_NONBLOCK, 0)
	if err == nil {
		f.Close() //nolint:errcheck // opening was the point; nothing was written
	}
}

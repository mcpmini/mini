//go:build test

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestCopyToClipboard_givesTheTextToThePlatformsTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "copied")
	tool := filepath.Join(dir, clipboardTools()[0][0])
	testutil.WriteFile(t, tool, "#!/bin/sh\nexec /bin/cat > "+out+"\n")
	if err := os.Chmod(tool, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := copyToClipboard("https://auth.example/linear"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := testutil.ReadFile(t, out); string(got) != "https://auth.example/linear" {
		t.Errorf("tool got %q, want the whole URL on its input", got)
	}
}

func TestCopyToClipboard_withNoToolSaysSo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := copyToClipboard("x"); !errors.Is(err, errNoClipboardTool) {
		t.Errorf("copy = %v, want errNoClipboardTool so the terminal is asked instead", err)
	}
}

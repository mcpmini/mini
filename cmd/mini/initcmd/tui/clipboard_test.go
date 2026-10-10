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
	fakeClipboardTool(t, dir, clipboardTools()[0][0], "exec /bin/cat > "+out)
	t.Setenv("PATH", dir)
	t.Setenv("SSH_CONNECTION", "")
	if err := copyToClipboard(t.Context(), "https://auth.example/linear"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := testutil.ReadFile(t, out); string(got) != "https://auth.example/linear" {
		t.Errorf("tool got %q, want the whole URL on its input", got)
	}
}

func TestCopyToClipboard_withNoToolSaysSo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := copyToClipboard(t.Context(), "x"); !errors.Is(err, errNoClipboardTool) {
		t.Errorf("copy = %v, want errNoClipboardTool so the terminal is asked instead", err)
	}
}

func fakeClipboardTool(t *testing.T, dir, name, script string) {
	t.Helper()
	tool := filepath.Join(dir, name)
	testutil.WriteFile(t, tool, "#!/bin/sh\n"+script+"\n")
	if err := os.Chmod(tool, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestCopyToClipboard_aFailingToolGivesTheNextOneATurn(t *testing.T) {
	tools := clipboardTools()
	if runtime.GOOS == "windows" || len(tools) < 2 {
		t.Skip("needs a platform with several clipboard tools and a shell")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "copied")
	fakeClipboardTool(t, dir, tools[0][0], "exit 1")
	fakeClipboardTool(t, dir, tools[1][0], "exec /bin/cat > "+out)
	t.Setenv("PATH", dir)
	t.Setenv("SSH_CONNECTION", "")
	if err := copyToClipboard(t.Context(), "https://auth.example/linear"); err != nil {
		t.Fatalf("copy: %v, want the second tool to copy", err)
	}
	if got := testutil.ReadFile(t, out); string(got) != "https://auth.example/linear" {
		t.Errorf("second tool got %q, want the whole URL", got)
	}
}

func TestCopyToClipboard_overSSHLeavesTheCopyToTheTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	fakeClipboardTool(t, dir, clipboardTools()[0][0], "exec /bin/cat > /dev/null")
	t.Setenv("PATH", dir)
	t.Setenv("SSH_CONNECTION", "10.0.0.1 50000 10.0.0.2 22")
	if err := copyToClipboard(t.Context(), "x"); !errors.Is(err, errNoClipboardTool) {
		t.Errorf("copy over SSH = %v, want errNoClipboardTool: the tool would fill the remote clipboard", err)
	}
}

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
	fakeClipboardTool(
		fakeClipboardToolParams{T: t, Dir: dir, Name: clipboardTools()[0][0], Script: "exec /bin/cat > " + out},
	)
	t.Setenv("PATH", dir)
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

func fakeClipboardTool(params fakeClipboardToolParams) {
	params.T.Helper()
	tool := filepath.Join(params.Dir, params.Name)
	testutil.WriteFile(params.T, tool, "#!/bin/sh\n"+params.Script+"\n")
	if err := os.Chmod(tool, 0o700); err != nil {
		params.T.Fatal(err)
	}
}

func TestCopyToClipboard_aFailingToolGivesTheNextOneATurn(t *testing.T) {
	tools := clipboardTools()
	if runtime.GOOS == "windows" || len(tools) < 2 {
		t.Skip("needs a platform with several clipboard tools and a shell")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "copied")
	fakeClipboardTool(fakeClipboardToolParams{T: t, Dir: dir, Name: tools[0][0], Script: "exit 1"})
	fakeClipboardTool(fakeClipboardToolParams{T: t, Dir: dir, Name: tools[1][0], Script: "exec /bin/cat > " + out})
	t.Setenv("PATH", dir)
	if err := copyToClipboard(t.Context(), "https://auth.example/linear"); err != nil {
		t.Fatalf("copy: %v, want the second tool to copy", err)
	}
	if got := testutil.ReadFile(t, out); string(got) != "https://auth.example/linear" {
		t.Errorf("second tool got %q, want the whole URL", got)
	}
}

type fakeClipboardToolParams struct {
	T      *testing.T
	Dir    string
	Name   string
	Script string
}

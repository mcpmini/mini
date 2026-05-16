package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSelfEntry(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("current executable is self", func(t *testing.T) {
		if !isSelfEntry(self, self) {
			t.Error("expected self to match self")
		}
	})
	t.Run("symlink to self is self", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "mini-link")
		if err := os.Symlink(self, link); err != nil {
			t.Skip("cannot create symlink:", err)
		}
		if !isSelfEntry(link, self) {
			t.Error("expected symlink to self to be detected as self")
		}
	})
	t.Run("unrelated binary is not self", func(t *testing.T) {
		if isSelfEntry("/usr/bin/env", self) {
			t.Error("expected /usr/bin/env to not be self")
		}
	})
	t.Run("empty cmd is not self", func(t *testing.T) {
		if isSelfEntry("", self) {
			t.Error("expected empty cmd to return false")
		}
	})
}

func TestImportClaudeFormat_SkipsSelf(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	claudeJSON := `{
		"projects": {
			"/some/path": {
				"mcpServers": {
					"github": {"type": "http", "url": "https://api.githubcopilot.com/mcp"},
					"mini":   {"command": "` + self + `", "args": ["proxy"]}
				}
			}
		}
	}`
	src := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(src, []byte(claudeJSON), 0600); err != nil {
		t.Fatal(err)
	}
	count := importClaudeFormat(configDir, src)
	if count != 1 {
		t.Errorf("imported %d servers, want 1 (mini should be skipped)", count)
	}
	if _, err := os.Stat(filepath.Join(configDir, "servers", "mini.yaml")); !os.IsNotExist(err) {
		t.Error("mini.yaml should not have been written")
	}
	if _, err := os.Stat(filepath.Join(configDir, "servers", "github.yaml")); err != nil {
		t.Error("github.yaml should have been written")
	}
}

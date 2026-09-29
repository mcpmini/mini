package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/importers"
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
					"mini":   {"command": "` + self + `", "args": ["connect"]}
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

func newTestPrompter(input string) (prompter, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return prompter{in: bufio.NewScanner(strings.NewReader(input)), out: out}, out
}

func TestPrompterAsk(t *testing.T) {
	t.Run("trims the answer", func(t *testing.T) {
		p, out := newTestPrompter("  hello \n")
		if got := p.ask("Q"); got != "hello" {
			t.Errorf("ask = %q, want hello", got)
		}
		if out.String() != "Q: " {
			t.Errorf("prompt = %q, want %q", out.String(), "Q: ")
		}
	})
	t.Run("empty on EOF", func(t *testing.T) {
		p, _ := newTestPrompter("")
		if got := p.ask("Q"); got != "" {
			t.Errorf("ask at EOF = %q, want empty", got)
		}
	})
}

func TestPrompterConfirm(t *testing.T) {
	for input, want := range map[string]bool{
		"y\n": true, "yes\n": true, "Y\n": true, "YES\n": true, " yes \n": true,
		"n\n": false, "\n": false, "": false, "yep\n": false,
	} {
		p, _ := newTestPrompter(input)
		if got := p.confirm("Q"); got != want {
			t.Errorf("confirm(%q) = %v, want %v", input, got, want)
		}
	}
	p, out := newTestPrompter("y\n")
	p.confirm("Import?")
	if out.String() != "Import? [y/N]: " {
		t.Errorf("confirm prompt = %q, want %q", out.String(), "Import? [y/N]: ")
	}
}

func TestAutoConfirmAccepts(t *testing.T) {
	if !autoConfirm("Q") {
		t.Error("autoConfirm = false, want true")
	}
}

func TestImportClaudeFormat_configuredServerIsNotCountedOrOverwritten(t *testing.T) {
	configDir := t.TempDir()
	if err := importers.AddServerYAML(configDir, "github", importers.ServerYAML{Command: "original"}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "claude.json")
	claudeJSON := `{"mcpServers":{"github":{"type":"http","url":"https://api.githubcopilot.com/mcp"}}}`
	if err := os.WriteFile(src, []byte(claudeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	if count := importClaudeFormat(configDir, src); count != 0 {
		t.Errorf("imported %d servers, want 0: github was already configured", count)
	}
	var sc importers.ServerYAML
	readServerYAML(t, configDir, "github", &sc)
	if sc.Command != "original" {
		t.Errorf("Command = %q, init overwrote the configured server", sc.Command)
	}
}

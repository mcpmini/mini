package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/testutil"
)

func claudeCodeAt(path string) agents.Agent {
	return agents.Agent{Name: "Claude Code", ConfigPath: path, Read: agents.ReadClaude}
}

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

func TestImportAgentConfig_SkipsSelf(t *testing.T) {
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
	testutil.WriteFile(t, src, claudeJSON)
	count := len(importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)))
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

func TestImportAgentConfig_NeverReplacesAConfiguredServer(t *testing.T) {
	tests := []struct {
		name      string
		reimport  string
		edit      func(path string) []byte
		wantLine  string
		forbidden string
	}{
		{
			name:     "same settings",
			reimport: `{"foo": {"type": "http", "url": "https://foo.example/mcp"}}`,
			wantLine: "Claude Code: foo already configured in mini",
		},
		{
			name:     "different settings",
			reimport: `{"foo": {"type": "http", "url": "https://other.example/mcp", "headers": {"Authorization": "Bearer secret-token"}}}`,
			wantLine: "Claude Code: foo not imported, mini's config has a different url, headers",
			// Header values are usually tokens.
			forbidden: "secret-token",
		},
		{
			name:     "env in another order, stdio written out",
			reimport: `{"foo": {"command": "foo-server", "env": {"B": "2", "A": "1"}}}`,
			edit: func(path string) []byte {
				return []byte("transport: stdio\ncommand: foo-server\nenv:\n  - A=1\n  - B=2\n")
			},
			wantLine: "Claude Code: foo already configured in mini",
		},
		{
			name:     "configured file does not parse",
			reimport: `{"foo": {"type": "http", "url": "https://foo.example/mcp"}}`,
			edit: func(path string) []byte {
				return []byte("headers:\n  Authorization: !!int secret-token\n")
			},
			wantLine:  "Claude Code: foo not imported, could not compare it with ",
			forbidden: "secret-token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			src := filepath.Join(t.TempDir(), "claude.json")
			testutil.WriteFile(t, src, `{"mcpServers": {"foo": {"type": "http", "url": "https://foo.example/mcp"}}}`)
			testutil.CaptureStdout(t, func() { importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)) })
			serverFile := filepath.Join(configDir, "servers", "foo.yaml")
			if tt.edit != nil {
				testutil.WriteFileBytes(t, serverFile, tt.edit(serverFile))
			}
			before := testutil.ReadFile(t, serverFile)
			testutil.WriteFile(t, src, `{"mcpServers": `+tt.reimport+`}`)

			var imported []string
			out := testutil.CaptureStdout(t, func() { imported = importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)) })

			after := testutil.ReadFile(t, serverFile)
			if len(imported) != 0 || string(after) != string(before) {
				t.Errorf("imported %v, foo.yaml %q -> %q; want nothing imported and the file unchanged", imported, before, after)
			}
			if !strings.Contains(out, tt.wantLine) {
				t.Errorf("stdout %q missing %q", out, tt.wantLine)
			}
			if tt.forbidden != "" && strings.Contains(out, tt.forbidden) {
				t.Errorf("stdout %q shows %q", out, tt.forbidden)
			}
		})
	}
}

func TestImportAgentConfig_ImportsOnlyNewServers(t *testing.T) {
	configDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "claude.json")
	testutil.WriteFile(t, src, `{"mcpServers": {"foo": {"type": "http", "url": "https://foo.example/mcp"}}}`)
	testutil.CaptureStdout(t, func() { importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)) })
	testutil.WriteFile(t, src, `{"mcpServers": {
		"foo": {"type": "http", "url": "https://foo.example/mcp"},
		"bar": {"type": "http", "url": "https://bar.example/mcp"}}}`)

	var imported []string
	testutil.CaptureStdout(t, func() { imported = importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)) })

	if !slices.Equal(imported, []string{"bar"}) {
		t.Errorf("second import = %v, want [bar] (only bar is new)", imported)
	}
	if _, err := os.Stat(filepath.Join(configDir, "servers", "bar.yaml")); err != nil {
		t.Errorf("bar.yaml not written: %v", err)
	}
}

func TestFindKnownAgent_missingHomeDoesNotUseWorkingDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("platform supplies a default home directory")
	}
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, ".claude.json", `{"mcpServers":{"example":{"url":"https://example.com/mcp"}}}`)
	if agent, found := findKnownAgent("Claude Code"); found || agent.ConfigPath != "" {
		t.Fatalf("agent = %+v, found = %v; want no agent without a home directory", agent, found)
	}
}

func TestResolveFromSource_explicitFileWorksWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	path := filepath.Join(t.TempDir(), "agent.json")
	testutil.WriteFile(t, path, `{"mcpServers":{"example":{"url":"https://example.com/mcp"}}}`)
	agent := resolveFromSource(path)
	if agent.ConfigPath != path || agent.Read == nil {
		t.Fatalf("source = %+v, want an explicit-file reader for %q", agent, path)
	}
	servers, err := agent.Read(agent.ConfigPath)
	if err != nil || servers["example"].URL != "https://example.com/mcp" {
		t.Fatalf("servers = %+v, error = %v; want the explicit file's server", servers, err)
	}
}

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

func TestImportAgentConfig_SkipsSelf(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	claudeJSON := `{
		"mcpServers": {
			"github": {"type": "http", "url": "https://api.githubcopilot.com/mcp"},
			"mini":   {"command": "` + self + `", "args": ["connect"]}
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
			wantLine:  "Claude Code: foo not imported, could not compare it: ",
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
			out := testutil.CaptureStdout(
				t,
				func() { imported = importAgentConfig(configDir, "Claude Code", claudeCodeAt(src)) },
			)

			after := testutil.ReadFile(t, serverFile)
			if len(imported) != 0 || string(after) != string(before) {
				t.Errorf(
					"imported %v, foo.yaml %q -> %q; want nothing imported and the file unchanged",
					imported,
					before,
					after,
				)
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

func TestInitImportsCodexAndNamesWhatMiniDoesNotCarryOver(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(home, ".codex", "config.toml"), `
[mcp_servers.search]
url = "https://search.example/mcp"
bearer_token_env_var = "SEARCH_TOKEN"

[mcp_servers.files]
command = "files-server"
cwd = "/srv/files"

[mcp_servers.tickets]
url = "https://tickets.example/mcp"
enabled_tools = ["read"]

[mcp_servers.paused]
command = "paused-server"
enabled = false

[mcp_servers.templated]
command = "run"
args = ["${HOME}/server.js"]

[mcp_servers.team]
url = "https://team.example/mcp"
http_headers = { X-Team = "default" }
env_http_headers = { X-Team = "MINI_TEST_NEVER_SET" }
`)
	t.Setenv("SEARCH_TOKEN", "")
	os.Unsetenv("SEARCH_TOKEN") //nolint:errcheck // t.Setenv above restores it after the test
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--import"})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	written := testutil.ReadFile(t, filepath.Join(configDir, "servers", "search.yaml"))
	if !strings.Contains(string(written), "Bearer ${SEARCH_TOKEN}") {
		t.Errorf("search.yaml = %s, want the bearer token kept as a reference", written)
	}
	for _, name := range []string{"files", "tickets"} {
		if _, err := os.Stat(filepath.Join(configDir, "servers", name+".yaml")); err != nil {
			t.Errorf("%s was not imported: %v", name, err)
		}
	}
	for _, name := range []string{"paused", "templated"} {
		if _, err := os.Stat(filepath.Join(configDir, "servers", name+".yaml")); !os.IsNotExist(err) {
			t.Errorf("%s was imported: %v", name, err)
		}
	}
	for _, want := range []string{
		"mini is set up with 4 servers, 1 still needs finishing:",
		"search  needs SEARCH_TOKEN set where mini runs (used in headers.Authorization), or edit " +
			filepath.Join(configDir, "servers", "search.yaml"),
		"files was imported without its cwd, which mini doesn't support yet; if it fails to start, edit " +
			filepath.Join(configDir, "servers", "files.yaml"),
		"[mcp_servers.mini]",
		"  templated kept in Codex: uses an environment variable in command or args",
		"team was imported with its static X-Team header, since MINI_TEST_NEVER_SET wasn't set; to use MINI_TEST_NEVER_SET instead, set X-Team: ${MINI_TEST_NEVER_SET} in " +
			filepath.Join(configDir, "servers", "team.yaml"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
}

func TestInitFromATOMLPathReadsCodexFormat(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "team.toml")
	testutil.WriteFile(t, src, "[mcp_servers.search]\nurl = \"https://search.example/mcp\"\n")
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--from", src})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := os.Stat(filepath.Join(configDir, "servers", "search.yaml")); err != nil {
		t.Errorf("search.yaml not written: %v\n%s", err, out)
	}
}

func TestFindKnownAgent_missingHomeDoesNotUseWorkingDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("CODEX_HOME", "")
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
	t.Setenv("CODEX_HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	path := filepath.Join(t.TempDir(), "agent.json")
	testutil.WriteFile(t, path, `{"mcpServers":{"example":{"url":"https://example.com/mcp"}}}`)
	agent, err := resolveFromSource(path)
	if err != nil || agent.ConfigPath != path || agent.Read == nil {
		t.Fatalf("source = %+v, %v; want an explicit-file reader for %q", agent, err, path)
	}
	servers, err := agent.Read(agent.ConfigPath)
	if err != nil || servers["example"].Config.URL != "https://example.com/mcp" {
		t.Fatalf("servers = %+v, error = %v; want the explicit file's server", servers, err)
	}
}

func TestShellQuoted(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/mini": "/usr/local/bin/mini",
		"/Users/a b/bin/mini": "'/Users/a b/bin/mini'",
		"/opt/it's/mini":      `'/opt/it'\''s/mini'`,
		"/opt/$HOME/mini":     "'/opt/$HOME/mini'",
	} {
		if got := shellQuoted(in); got != want {
			t.Errorf("shellQuoted(%q) = %s, want %s", in, got, want)
		}
	}
}

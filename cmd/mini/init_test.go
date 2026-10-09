package main

import (
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

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
		"  paused switched off in Codex",
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

func TestImportSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	testutil.WriteFile(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{}}`)
	team := filepath.Join(t.TempDir(), "team.json")
	testutil.WriteFile(t, team, `{"mcpServers":{}}`)
	for name, tt := range map[string]struct {
		flags initFlags
		want  []string
	}{
		"--add alone imports nothing":         {initFlags{add: []string{"notion"}, addGiven: true}, nil},
		"--import reads every detected agent": {initFlags{importAll: true}, []string{"Cursor"}},
		"--from reads only its source":        {initFlags{from: team}, []string{team}},
	} {
		t.Run(name, func(t *testing.T) {
			sources, err := importSources(tt.flags)
			var got []string
			for _, source := range sources {
				got = append(got, source.Name)
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("sources = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	t.Run("an unreadable --from source is an error", func(t *testing.T) {
		if _, err := importSources(initFlags{from: filepath.Join(t.TempDir(), "missing.json")}); err == nil {
			t.Error("want an error for a source that can't be read")
		}
	})
}

func TestInitImportWritesEachConfigOnceAndLeavesTheAgentsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	claudePath := filepath.Join(home, ".claude.json")
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	testutil.WriteFile(t, claudePath, `{"mcpServers":{"github":{"command":"gh-server"}}}`)
	testutil.WriteFile(
		t,
		cursorPath,
		`{"mcpServers":{"GitHub MCP":{"command":"gh-server"},"Notes":{"command":"notes-server"}}}`,
	)
	before := filesUnder(t, home)
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--import"})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if got := slices.Sorted(maps.Keys(filesUnder(t, filepath.Join(configDir, "servers")))); !slices.Equal(
		got, []string{"github.yaml", "notes.yaml"}) {
		t.Errorf("server files = %v, want github once for both agents, and notes", got)
	}
	if after := filesUnder(t, home); !maps.Equal(after, before) {
		t.Errorf("home changed during init --import:\nbefore %v\nafter  %v", before, after)
	}
	for _, want := range []string{"claude mcp add --scope user mini", "Cursor (" + cursorPath + ")"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing the hand-connect step %q:\n%s", want, out)
		}
	}
}

func filesUnder(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		files[rel] = string(testutil.ReadFile(t, path))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestIsTerminalRejectsNullDevice(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("null device was classified as an interactive terminal")
	}
}

func upstreamAnswering(t *testing.T, status int, challenge string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if challenge != "" {
			w.Header().Set("WWW-Authenticate", challenge)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestInitCommandDetectsOAuthOnImportedServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	src := filepath.Join(t.TempDir(), "claude.json")
	testutil.WriteFile(t, src, `{"mcpServers": {"svc": {"type": "http", "url": "`+url+`"}}}`)
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--from", src})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "1 still needs finishing") || !strings.Contains(out, "auth svc") {
		t.Errorf("init output = %q, want svc listed as needing a login", out)
	}
}

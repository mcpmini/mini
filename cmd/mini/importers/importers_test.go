//go:build test

package importers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

func TestExtractClaudeMCPServers(t *testing.T) {
	t.Run("desktop format", func(t *testing.T) {
		data := []byte(`{"mcpServers":{"gh":{"command":"npx","args":["server-github"]}}}`)
		servers := ExtractClaudeMCPServers(data)
		if _, ok := servers["gh"]; !ok {
			t.Fatalf("expected server 'gh', got %v", servers)
		}
	})

	t.Run("claude code format", func(t *testing.T) {
		data := []byte(`{"projects":{"/home/user/proj":{"mcpServers":{"myserver":{"command":"run"}}}}}`)
		servers := ExtractClaudeMCPServers(data)
		if _, ok := servers["myserver"]; !ok {
			t.Fatalf("expected server 'myserver', got %v", servers)
		}
	})

	t.Run("empty json", func(t *testing.T) {
		servers := ExtractClaudeMCPServers([]byte(`{}`))
		if len(servers) != 0 {
			t.Fatalf("expected empty, got %v", servers)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		servers := ExtractClaudeMCPServers([]byte(`not json`))
		if len(servers) != 0 {
			t.Fatalf("expected empty for invalid json, got %v", servers)
		}
	})

	t.Run("duplicate server across projects keeps first", func(t *testing.T) {
		data := []byte(`{"projects":{
			"/a":{"mcpServers":{"dup":{"command":"first"}}},
			"/b":{"mcpServers":{"dup":{"command":"second"}}}
		}}`)
		servers := ExtractClaudeMCPServers(data)
		if len(servers) != 1 {
			t.Fatalf("expected 1 server after dedup, got %d", len(servers))
		}
	})
}

func TestClaudeEntryToServer(t *testing.T) {
	tests := []struct {
		name      string
		entry     ClaudeMCPEntry
		wantHTTP  bool
		wantURL   string
		wantCmd   string
	}{
		{
			name:     "http by url",
			entry:    ClaudeMCPEntry{URL: "https://api.github.com/mcp"},
			wantHTTP: true,
			wantURL:  "https://api.github.com/mcp",
		},
		{
			name:     "http by type http",
			entry:    ClaudeMCPEntry{Type: "http", URL: "https://example.com"},
			wantHTTP: true,
			wantURL:  "https://example.com",
		},
		{
			name:     "http by type sse",
			entry:    ClaudeMCPEntry{Type: "sse", URL: "https://sse.example.com"},
			wantHTTP: true,
			wantURL:  "https://sse.example.com",
		},
		{
			name:    "stdio with command and args",
			entry:   ClaudeMCPEntry{Command: "npx", Args: []string{"-y", "server-github"}},
			wantCmd: "npx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := ClaudeEntryToServer("myserver", tt.entry)
			if sc.Name != "myserver" {
				t.Errorf("Name = %q, want 'myserver'", sc.Name)
			}
			if tt.wantHTTP {
				if sc.Transport != "http" {
					t.Errorf("Transport = %q, want 'http'", sc.Transport)
				}
				if sc.URL != tt.wantURL {
					t.Errorf("URL = %q, want %q", sc.URL, tt.wantURL)
				}
			} else {
				if sc.Command != tt.wantCmd {
					t.Errorf("Command = %q, want %q", sc.Command, tt.wantCmd)
				}
			}
		})
	}

	t.Run("env map is converted to KEY=VALUE list", func(t *testing.T) {
		entry := ClaudeMCPEntry{Command: "run", Env: map[string]string{"FOO": "bar"}}
		sc := ClaudeEntryToServer("s", entry)
		if len(sc.Env) != 1 || sc.Env[0] != "FOO=bar" {
			t.Errorf("Env = %v, want [FOO=bar]", sc.Env)
		}
	})
}

func TestImportFromClaude(t *testing.T) {
	t.Run("creates server yaml", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "claude.json")
		os.WriteFile(f, []byte(`{"mcpServers":{"gh":{"command":"npx","args":["server-github"]}}}`), 0600)

		if err := ImportFromClaude(dir, f); err != nil {
			t.Fatalf("ImportFromClaude: %v", err)
		}
		var sc ServerYAML
		readYAML(t, filepath.Join(dir, "servers", "gh.yaml"), &sc)
		if sc.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", sc.Command)
		}
	})

	t.Run("empty servers prints message", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "empty.json")
		os.WriteFile(f, []byte(`{}`), 0600)
		if err := ImportFromClaude(dir, f); err != nil {
			t.Fatalf("ImportFromClaude: %v", err)
		}
	})
}

func TestImportFromCursor(t *testing.T) {
	dir := tempDir(t)
	f := filepath.Join(dir, "mcp.json")
	os.WriteFile(f, []byte(`{"mcpServers":{"linear":{"url":"https://linear.app/mcp","type":"http"}}}`), 0600)

	if err := ImportFromCursor(dir, f); err != nil {
		t.Fatalf("ImportFromCursor: %v", err)
	}
	var sc ServerYAML
	readYAML(t, filepath.Join(dir, "servers", "linear.yaml"), &sc)
	if sc.Transport != "http" {
		t.Errorf("Transport = %q, want 'http'", sc.Transport)
	}
}

func TestImportFromGemini(t *testing.T) {
	t.Run("http entry", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "settings.json")
		os.WriteFile(f, []byte(`{"mcpServers":{"github":{"httpUrl":"https://api.github.com/mcp"}}}`), 0600)

		if err := ImportFromGemini(dir, f); err != nil {
			t.Fatalf("ImportFromGemini: %v", err)
		}
		var sc ServerYAML
		readYAML(t, filepath.Join(dir, "servers", "github.yaml"), &sc)
		if sc.Transport != "http" || sc.URL != "https://api.github.com/mcp" {
			t.Errorf("got Transport=%q URL=%q", sc.Transport, sc.URL)
		}
	})

	t.Run("command entry", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "settings.json")
		os.WriteFile(f, []byte(`{"mcpServers":{"local":{"command":"node","args":["server.js"]}}}`), 0600)

		if err := ImportFromGemini(dir, f); err != nil {
			t.Fatalf("ImportFromGemini: %v", err)
		}
		var sc ServerYAML
		readYAML(t, filepath.Join(dir, "servers", "local.yaml"), &sc)
		if sc.Command != "node" {
			t.Errorf("Command = %q, want 'node'", sc.Command)
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "bad.json")
		os.WriteFile(f, []byte(`not json`), 0600)
		if err := ImportFromGemini(dir, f); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestImportFromCodex(t *testing.T) {
	t.Run("stdio entry", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "config.toml")
		os.WriteFile(f, []byte("[mcp_servers.gh]\ncommand = \"npx\"\nargs = [\"-y\", \"server-github\"]\n"), 0600)

		if err := ImportFromCodex(dir, f); err != nil {
			t.Fatalf("ImportFromCodex: %v", err)
		}
		var sc ServerYAML
		readYAML(t, filepath.Join(dir, "servers", "gh.yaml"), &sc)
		if sc.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", sc.Command)
		}
	})

	t.Run("http entry via url", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "config.toml")
		os.WriteFile(f, []byte("[mcp_servers.sentry]\nurl = \"https://mcp.sentry.io\"\n"), 0600)

		if err := ImportFromCodex(dir, f); err != nil {
			t.Fatalf("ImportFromCodex: %v", err)
		}
		var sc ServerYAML
		readYAML(t, filepath.Join(dir, "servers", "sentry.yaml"), &sc)
		if sc.Transport != "http" {
			t.Errorf("Transport = %q, want 'http'", sc.Transport)
		}
	})

	t.Run("invalid toml returns error", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "bad.toml")
		os.WriteFile(f, []byte("not = toml = invalid\n"), 0600)
		if err := ImportFromCodex(dir, f); err == nil {
			t.Fatal("expected error for invalid TOML")
		}
	})
}

func TestWriteServerYAML(t *testing.T) {
	t.Run("valid name creates file with correct content", func(t *testing.T) {
		dir := tempDir(t)
		sc := ServerYAML{Name: "myserver", Command: "npx", Args: []string{"server-github"}}

		if err := WriteServerYAML(dir, "myserver", sc); err != nil {
			t.Fatalf("WriteServerYAML: %v", err)
		}
		path := filepath.Join(dir, "servers", "myserver.yaml")
		var got ServerYAML
		readYAML(t, path, &got)
		if got.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", got.Command)
		}
		if len(got.Args) != 1 || got.Args[0] != "server-github" {
			t.Errorf("Args = %v, want [server-github]", got.Args)
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		err := WriteServerYAML(dir, "bad name!", ServerYAML{})
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
		if !strings.Contains(err.Error(), "invalid server name") {
			t.Errorf("error message = %q, want to contain 'invalid server name'", err.Error())
		}
	})

	t.Run("file permissions are 0600", func(t *testing.T) {
		dir := tempDir(t)
		if err := WriteServerYAML(dir, "sec", ServerYAML{Name: "sec"}); err != nil {
			t.Fatalf("WriteServerYAML: %v", err)
		}
		info, err := os.Stat(filepath.Join(dir, "servers", "sec.yaml"))
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("perm = %04o, want 0600", perm)
		}
	})
}

func TestReadConfigFile(t *testing.T) {
	t.Run("file not found returns error", func(t *testing.T) {
		_, err := ReadConfigFile("/nonexistent/path/file.json")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("happy path returns contents", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "test.json")
		want := []byte(`{"hello":"world"}`)
		os.WriteFile(f, want, 0600)

		got, err := ReadConfigFile(f)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("file too large returns error", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "big.json")
		big := make([]byte, maxImportConfigBytes+1)
		os.WriteFile(f, big, 0600)

		_, err := ReadConfigFile(f)
		if err == nil {
			t.Fatal("expected error for oversized file")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("error = %q, want 'too large'", err.Error())
		}
	})
}


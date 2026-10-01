package importers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func writeClientConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(tempDir(t), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadClientConfigs(t *testing.T) {
	tests := []struct {
		name   string
		read   func(string) (map[string]config.ServerConfig, error)
		file   string
		config string
		want   map[string]config.ServerConfig
	}{
		{
			name:   "claude desktop stdio entry, env as a sorted KEY=VALUE list",
			read:   ReadClaude,
			file:   "claude.json",
			config: `{"mcpServers":{"gh":{"command":"npx","args":["server-github"],"env":{"B":"2","A":"1"}}}}`,
			want:   map[string]config.ServerConfig{"gh": {Name: "gh", Command: "npx", Args: []string{"server-github"}, Env: []string{"A=1", "B=2"}}},
		},
		{
			name:   "claude code project entries",
			read:   ReadClaude,
			file:   "claude.json",
			config: `{"projects":{"/home/user/proj":{"mcpServers":{"local":{"command":"run"}}}}}`,
			want:   map[string]config.ServerConfig{"local": {Name: "local", Command: "run"}},
		},
		{
			name:   "claude http entry by url keeps headers",
			read:   ReadClaude,
			file:   "claude.json",
			config: `{"mcpServers":{"gh":{"url":"https://api.github.com/mcp","headers":{"Authorization":"Bearer ${GH}"}}}}`,
			want:   map[string]config.ServerConfig{"gh": {Name: "gh", Transport: "http", URL: "https://api.github.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${GH}"}}},
		},
		{
			name:   "claude sse type is http",
			read:   ReadClaude,
			file:   "claude.json",
			config: `{"mcpServers":{"s":{"type":"sse","url":"https://sse.example.com"}}}`,
			want:   map[string]config.ServerConfig{"s": {Name: "s", Transport: "http", URL: "https://sse.example.com"}},
		},
		{
			name:   "codex stdio entry",
			read:   ReadCodex,
			file:   "config.toml",
			config: "[mcp_servers.gh]\ncommand = \"npx\"\nargs = [\"-y\", \"server-github\"]\n",
			want:   map[string]config.ServerConfig{"gh": {Name: "gh", Command: "npx", Args: []string{"-y", "server-github"}}},
		},
		{
			name:   "codex http entry via url",
			read:   ReadCodex,
			file:   "config.toml",
			config: "[mcp_servers.sentry]\nurl = \"https://mcp.sentry.io\"\n",
			want:   map[string]config.ServerConfig{"sentry": {Name: "sentry", Transport: "http", URL: "https://mcp.sentry.io"}},
		},
		{
			name:   "gemini httpUrl entry",
			read:   ReadGemini,
			file:   "settings.json",
			config: `{"mcpServers":{"github":{"httpUrl":"https://api.github.com/mcp"}}}`,
			want:   map[string]config.ServerConfig{"github": {Name: "github", Transport: "http", URL: "https://api.github.com/mcp"}},
		},
		{
			name:   "gemini command entry",
			read:   ReadGemini,
			file:   "settings.json",
			config: `{"mcpServers":{"local":{"command":"node","args":["server.js"]}}}`,
			want:   map[string]config.ServerConfig{"local": {Name: "local", Command: "node", Args: []string{"server.js"}}},
		},
		{
			name:   "openclaw stdio entry",
			read:   ReadOpenClaw,
			file:   "openclaw.json",
			config: `{"mcp":{"servers":{"fs":{"command":"npx","env":{"ROOT":"/data"}}}}}`,
			want:   map[string]config.ServerConfig{"fs": {Name: "fs", Command: "npx", Env: []string{"ROOT=/data"}}},
		},
		{
			name:   "openclaw http entry",
			read:   ReadOpenClaw,
			file:   "openclaw.json",
			config: `{"mcp":{"servers":{"remote":{"url":"https://example.com/mcp"}}}}`,
			want:   map[string]config.ServerConfig{"remote": {Name: "remote", Transport: "http", URL: "https://example.com/mcp"}},
		},
		{
			name:   "no servers is an empty result, not an error",
			read:   ReadGemini,
			file:   "settings.json",
			config: `{}`,
			want:   map[string]config.ServerConfig{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.read(writeClientConfig(t, tt.file, tt.config))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestReadClientConfigs_unparsableConfigIsAnError(t *testing.T) {
	for name, read := range map[string]func(string) (map[string]config.ServerConfig, error){
		"codex": ReadCodex, "gemini": ReadGemini, "openclaw": ReadOpenClaw,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := read(writeClientConfig(t, "bad", "not = valid = anything {")); err == nil {
				t.Fatal("expected a parse error")
			}
		})
	}
}

func TestReadClaude_duplicateServerAcrossProjectsKeepsOne(t *testing.T) {
	path := writeClientConfig(t, "claude.json", `{"projects":{
		"/a":{"mcpServers":{"dup":{"command":"first"}}},
		"/b":{"mcpServers":{"dup":{"command":"second"}}}
	}}`)
	got, err := ReadClaude(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadClaude = %v, %v; want the one dup server", got, err)
	}
}

func TestReadClaude_missingFileIsAnError(t *testing.T) {
	if _, err := ReadClaude(filepath.Join(tempDir(t), "missing.json")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
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

	t.Run("file at the limit is returned whole", func(t *testing.T) {
		f := filepath.Join(tempDir(t), "limit.json")
		os.WriteFile(f, make([]byte, maxImportConfigBytes), 0600)

		got, err := ReadConfigFile(f)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if len(got) != maxImportConfigBytes {
			t.Errorf("read %d bytes, want %d", len(got), maxImportConfigBytes)
		}
	})
}

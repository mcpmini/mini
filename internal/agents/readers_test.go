package agents

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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
	testutil.WriteFile(t, path, content)
	return path
}

type readerFunc func(string) (map[string]Server, error)

func stdio(name, command string, args ...string) config.ServerConfig {
	return config.ServerConfig{Name: name, Command: command, Args: args}
}

func remote(name, url string, headers map[string]string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url, Headers: headers}
}

func TestReadClientConfigs(t *testing.T) {
	tests := []struct {
		name   string
		read   readerFunc
		file   string
		config string
		want   Server
	}{
		{"claude desktop stdio entry, env as a sorted KEY=VALUE list", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"command":"npx","args":["server-github"],"env":{"B":"2","A":"1"}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "npx", Args: []string{"server-github"}, Env: []string{"A=1", "B=2"}}}},
		{"claude code project entries", ReadClaude, "claude.json",
			`{"projects":{"/home/user/proj":{"mcpServers":{"s":{"command":"run"}}}}}`,
			Server{Config: stdio("s", "run")}},
		{"claude http entry by url keeps headers", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${GH}"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${GH}"})}},
		{"claude sse type is http", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"type":"sse","url":"https://sse.example.com"}}}`,
			Server{Config: remote("s", "https://sse.example.com", nil)}},
		{"claude ${VAR} in args is kept in the agent", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"command":"run","args":["--root","${HOME}/src"]}}}`,
			Server{Config: stdio("s", "run", "--root", "${HOME}/src"), UnexpandableRefs: []string{"an environment variable in command or args"}}},
		{"claude $ in args is literal, as Claude Code passes it", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"command":"grep","args":["^end$"]}}}`,
			Server{Config: stdio("s", "grep", "^end$")}},
		{"claude default-value syntax is kept in the agent", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${GH:-none}"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${GH:-none}"}), UnexpandableRefs: []string{"an environment variable syntax mini doesn't read"}}},
		{"cursor ${env:VAR} becomes ${VAR}", ReadClaude, "mcp.json",
			`{"mcpServers":{"s":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${env:API_KEY}"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${API_KEY}"})}},
		{"cursor envFile is ignored and named", ReadClaude, "mcp.json",
			`{"mcpServers":{"s":{"command":"run","envFile":".env"}}}`,
			Server{Config: stdio("s", "run"), IgnoredRunSettings: []string{"envFile"}}},
		{"windsurf serverUrl is the url", ReadClaude, "mcp_config.json",
			`{"mcpServers":{"s":{"serverUrl":"https://example.com/mcp"}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil)}},
		{"windsurf disabled and disabledTools", ReadClaude, "mcp_config.json",
			`{"mcpServers":{"s":{"command":"run","disabled":true,"disabledTools":["delete"]}}}`,
			Server{Config: stdio("s", "run"), Disabled: true}},
		{"codex stdio entry", ReadCodex, "config.toml",
			"[mcp_servers.s]\ncommand = \"npx\"\nargs = [\"-y\", \"server-github\"]\nenv = { TOKEN = \"synthetic\" }\n",
			Server{Config: config.ServerConfig{Name: "s", Command: "npx", Args: []string{"-y", "server-github"}, Env: []string{"TOKEN=synthetic"}}}},
		{"codex http headers, env headers and bearer token variable", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { X-Team = \"core\" }\nenv_http_headers = { X-Key = \"MINI_TEST_SET_KEY\", X-Optional = \"MINI_TEST_UNSET_KEY\" }\nbearer_token_env_var = \"EXAMPLE_TOKEN\"\n",
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{
				"X-Team": "core", "X-Key": "${MINI_TEST_SET_KEY}", "X-Optional": "${MINI_TEST_UNSET_KEY}", "Authorization": "Bearer ${EXAMPLE_TOKEN}",
			})}},
		{"codex env and bearer headers replace a static header whatever its case", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { authorization = \"Bearer old\", x-key = \"old\" }\n" +
				"env_http_headers = { X-Key = \"MINI_TEST_SET_KEY\" }\nbearer_token_env_var = \"TOKEN_VAR\"\n",
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"X-Key": "${MINI_TEST_SET_KEY}", "Authorization": "Bearer ${TOKEN_VAR}"})}},
		{"codex keeps a static header whatever its case while its env override is unset", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { x-team = \"default\" }\n" +
				"env_http_headers = { X-Team = \"MINI_TEST_UNSET_KEY\" }\n",
			Server{
				Config:           remote("s", "https://example.com/mcp", map[string]string{"x-team": "default"}),
				UnusedEnvHeaders: map[string]string{"X-Team": "MINI_TEST_UNSET_KEY"},
			}},
		{"codex header helpers are dropped and named", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers_helper = \"get-token\"\n",
			Server{Config: remote("s", "https://example.com/mcp", nil), IgnoredRunSettings: []string{"http_headers_helper"}}},
		{"openclaw cwd and tls settings are dropped and named", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","cwd":"/srv","sslVerify":false}}}}`,
			Server{Config: stdio("s", "npx"), IgnoredRunSettings: []string{"cwd", "sslVerify"}}},
		{"codex keeps a static header while its env override is unset or blank", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { X-Team = \"default\", X-Org = \"acme\" }\n" +
				"env_http_headers = { X-Team = \"MINI_TEST_UNSET_KEY\", X-Org = \"MINI_TEST_BLANK_KEY\" }\n",
			Server{
				Config:           remote("s", "https://example.com/mcp", map[string]string{"X-Team": "default", "X-Org": "acme"}),
				UnusedEnvHeaders: map[string]string{"X-Org": "MINI_TEST_BLANK_KEY", "X-Team": "MINI_TEST_UNSET_KEY"},
			}},
		{"codex variable named like an editor placeholder is a plain reference", ReadCodex, "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nbearer_token_env_var = \"userHome\"\n",
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${userHome}"})}},
		{"codex switched off", ReadCodex, "config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\nenabled = false\n",
			Server{Config: stdio("s", "run"), Disabled: true}},
		{"codex settings mini doesn't carry over are dropped, including ones it has never seen", ReadCodex, "config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\ntool_timeout_sec = 60\nenabled_tools = [\"search\"]\n" +
				"default_tools_approval_mode = \"prompt\"\nsetting_added_later = true\n",
			Server{Config: stdio("s", "run")}},
		{"codex cwd is ignored and named", ReadCodex, "config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\ncwd = \"/srv/app\"\n",
			Server{Config: stdio("s", "run"), IgnoredRunSettings: []string{"cwd"}}},
		{"cursor editor placeholders are kept in the agent", ReadClaude, "mcp.json",
			`{"mcpServers":{"s":{"command":"run","env":{"ROOT":"${workspaceFolder}/data"}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "run", Env: []string{"ROOT=${workspaceFolder}/data"}}, UnexpandableRefs: []string{"an editor placeholder like ${userHome}"}}},
		{"gemini httpUrl entry", ReadGemini, "settings.json",
			`{"mcpServers":{"s":{"httpUrl":"https://example.com/mcp","timeout":30000}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil)}},
		{"gemini url (SSE) entry", ReadGemini, "settings.json",
			`{"mcpServers":{"s":{"url":"https://example.com/sse"}}}`,
			Server{Config: remote("s", "https://example.com/sse", nil)}},
		{"gemini $VAR becomes ${VAR}", ReadGemini, "settings.json",
			`{"mcpServers":{"s":{"command":"node","args":["server.js"],"env":{"TOKEN":"$EXAMPLE_TOKEN"}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "node", Args: []string{"server.js"}, Env: []string{"TOKEN=${EXAMPLE_TOKEN}"}}}},
		{"gemini $VAR in args is kept in the agent", ReadGemini, "settings.json",
			`{"mcpServers":{"s":{"command":"node","args":["$HOME/server.js"]}}}`,
			Server{Config: stdio("s", "node", "$HOME/server.js"), UnexpandableRefs: []string{"an environment variable in command or args"}}},
		{"gemini tool lists are dropped and cwd is named", ReadGemini, "settings.json",
			`{"mcpServers":{"s":{"command":"node","cwd":"/srv","includeTools":["read"]}}}`,
			Server{Config: stdio("s", "node"), IgnoredRunSettings: []string{"cwd"}}},
		{"openclaw stdio entry", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","env":{"ROOT":"/data"}}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "npx", Env: []string{"ROOT=/data"}}}},
		{"openclaw tool filter and approval settings are dropped", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","toolFilter":{"allow":["read"]},"codex":{"approval":"prompt"}}}}}`,
			Server{Config: stdio("s", "npx")}},
		{"openclaw http entry switched off", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"url":"https://example.com/mcp","enabled":false}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil), Disabled: true}},
	}
	t.Setenv("MINI_TEST_SET_KEY", "synthetic")
	t.Setenv("MINI_TEST_BLANK_KEY", " ")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.read(writeClientConfig(t, tt.file, tt.config))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if want := map[string]Server{"s": tt.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestReadClientConfigs_noServersIsAnEmptyResult(t *testing.T) {
	got, err := ReadGemini(writeClientConfig(t, "settings.json", `{}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("ReadGemini = %v, %v; want nothing and no error", got, err)
	}
}

func TestReadClientConfigs_unparsableConfigIsAnError(t *testing.T) {
	for name, read := range map[string]readerFunc{
		"claude": ReadClaude, "codex": ReadCodex, "gemini": ReadGemini, "openclaw": ReadOpenClaw,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := read(writeClientConfig(t, "bad", "not = valid = anything {")); err == nil {
				t.Fatal("expected a parse error")
			}
		})
	}
	t.Run("a malformed entry", func(t *testing.T) {
		if _, err := ReadClaude(writeClientConfig(t, "claude.json", `{"mcpServers":{"s":{"args":"not a list"}}}`)); err == nil {
			t.Fatal("expected a parse error")
		}
	})
	t.Run("a malformed codex entry", func(t *testing.T) {
		if _, err := ReadCodex(writeClientConfig(t, "config.toml", "[mcp_servers.s]\nargs = \"not a list\"\n")); err == nil {
			t.Fatal("expected a parse error")
		}
	})
}

func TestServerCandidate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		server Server
		want   bool
	}{
		{"plain", Server{}, true},
		{"switched off is still a candidate", Server{Disabled: true}, true},
		{"ignored settings are still a candidate", Server{IgnoredRunSettings: []string{"cwd"}}, true},
		{"unsupported reference", Server{UnexpandableRefs: []string{"an environment variable in url"}}, false},
	} {
		if got := tt.server.Candidate(); got != tt.want {
			t.Errorf("%s: Candidate() = %v, want %v", tt.name, got, tt.want)
		}
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
		testutil.WriteFileBytes(t, f, want)

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
		testutil.WriteFileBytes(t, f, big)

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
		testutil.WriteFileBytes(t, f, make([]byte, maxImportConfigBytes))

		got, err := ReadConfigFile(f)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if len(got) != maxImportConfigBytes {
			t.Errorf("read %d bytes, want %d", len(got), maxImportConfigBytes)
		}
	})
}

func TestKnownCodexConfigFollowsCodexHome(t *testing.T) {
	codexPath := func() string {
		for _, a := range Known("/home/user") {
			if a.Name == "Codex" {
				return a.ConfigPath
			}
		}
		return ""
	}
	t.Setenv("CODEX_HOME", "")
	if got := codexPath(); got != filepath.Join("/home/user", ".codex", "config.toml") {
		t.Errorf("default Codex config = %q, want ~/.codex/config.toml", got)
	}
	t.Setenv("CODEX_HOME", "/srv/codex")
	if got := codexPath(); got != filepath.Join("/srv/codex", "config.toml") {
		t.Errorf("Codex config with CODEX_HOME set = %q, want /srv/codex/config.toml", got)
	}
}

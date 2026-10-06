package agents

import (
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestCodexReader(t *testing.T) {
	tests := []readerCase{
		{
			"codex stdio entry",
			"config.toml",
			"[mcp_servers.s]\ncommand = \"npx\"\nargs = [\"-y\", \"server-github\"]\nenv = { TOKEN = \"synthetic\" }\n",
			Server{
				Config: config.ServerConfig{
					Name:    "s",
					Command: "npx",
					Args:    []string{"-y", "server-github"},
					Env:     []string{"TOKEN=synthetic"},
				},
			},
		},
		{
			"codex http headers, env headers and bearer token variable",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { X-Team = \"core\" }\nenv_http_headers = { X-Key = \"MINI_TEST_SET_KEY\", X-Optional = \"MINI_TEST_UNSET_KEY\" }\nbearer_token_env_var = \"EXAMPLE_TOKEN\"\n",
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{
				"X-Team":        "core",
				"X-Key":         "${MINI_TEST_SET_KEY}",
				"X-Optional":    "${MINI_TEST_UNSET_KEY}",
				"Authorization": "Bearer ${EXAMPLE_TOKEN}",
			})},
		},
		{
			"codex env and bearer headers replace a static header whatever its case",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { authorization = \"Bearer old\", x-key = \"old\" }\n" +
				"env_http_headers = { X-Key = \"MINI_TEST_SET_KEY\" }\nbearer_token_env_var = \"TOKEN_VAR\"\n",
			Server{
				Config: remote(
					"s",
					"https://example.com/mcp",
					map[string]string{"X-Key": "${MINI_TEST_SET_KEY}", "Authorization": "Bearer ${TOKEN_VAR}"},
				),
			},
		},
		{
			"codex keeps a static header whatever its case while its env override is unset", "config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { x-team = \"default\" }\n" +
				"env_http_headers = { X-Team = \"MINI_TEST_UNSET_KEY\" }\n",
			Server{
				Config:           remote("s", "https://example.com/mcp", map[string]string{"x-team": "default"}),
				UnusedEnvHeaders: map[string]string{"X-Team": "MINI_TEST_UNSET_KEY"},
			},
		},
		{
			"codex auth settings are dropped and named",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nscopes = [\"read\"]\noauth = { client_id = \"synthetic\" }\n",
			Server{
				Config:             remote("s", "https://example.com/mcp", nil),
				IgnoredRunSettings: []string{"oauth", "scopes"},
			},
		},
		{
			"codex header helpers are dropped and named",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers_helper = \"get-token\"\n",
			Server{
				Config:             remote("s", "https://example.com/mcp", nil),
				IgnoredRunSettings: []string{"http_headers_helper"},
			},
		},
		{
			"codex keeps a static header while its env override is unset or blank",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nhttp_headers = { X-Team = \"default\", X-Org = \"acme\" }\n" +
				"env_http_headers = { X-Team = \"MINI_TEST_UNSET_KEY\", X-Org = \"MINI_TEST_BLANK_KEY\" }\n",
			Server{
				Config: remote(
					"s",
					"https://example.com/mcp",
					map[string]string{"X-Team": "default", "X-Org": "acme"},
				),
				UnusedEnvHeaders: map[string]string{"X-Org": "MINI_TEST_BLANK_KEY", "X-Team": "MINI_TEST_UNSET_KEY"},
			},
		},
		{
			"codex variable named like an editor placeholder is a plain reference",
			"config.toml",
			"[mcp_servers.s]\nurl = \"https://example.com/mcp\"\nbearer_token_env_var = \"userHome\"\n",
			Server{
				Config: remote(
					"s",
					"https://example.com/mcp",
					map[string]string{"Authorization": "Bearer ${userHome}"},
				),
			},
		},
		{
			"codex switched off", "config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\nenabled = false\n",
			Server{Config: stdio("s", "run"), Disabled: true},
		},
		{
			"codex settings mini doesn't carry over are dropped, including ones it has never seen",
			"config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\ntool_timeout_sec = 60\nenabled_tools = [\"search\"]\n" +
				"default_tools_approval_mode = \"prompt\"\nsetting_added_later = true\n",
			Server{Config: stdio("s", "run")},
		},
		{
			"codex cwd is ignored and named", "config.toml",
			"[mcp_servers.s]\ncommand = \"run\"\ncwd = \"/srv/app\"\n",
			Server{Config: stdio("s", "run"), IgnoredRunSettings: []string{"cwd"}},
		},
	}
	t.Setenv("MINI_TEST_SET_KEY", "synthetic")
	t.Setenv("MINI_TEST_BLANK_KEY", " ")
	runReaderCases(t, ReadCodex, tests)
}

func TestReadCodex_aMalformedEntryIsAnError(t *testing.T) {
	if _, err := ReadCodex(
		writeClientConfig(t, "config.toml", "[mcp_servers.s]\nargs = \"not a list\"\n"),
	); err == nil {
		t.Fatal("expected a parse error")
	}
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

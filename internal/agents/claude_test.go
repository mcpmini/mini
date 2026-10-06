package agents

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestClaudeReader(t *testing.T) {
	tests := []readerCase{
		{
			"claude desktop stdio entry, env as a sorted KEY=VALUE list",
			"claude.json",
			`{"mcpServers":{"s":{"command":"npx","args":["server-github"],"env":{"B":"2","A":"1"}}}}`,
			Server{
				Config: config.ServerConfig{
					Name:    "s",
					Command: "npx",
					Args:    []string{"server-github"},
					Env:     []string{"A=1", "B=2"},
				},
			},
		},
		{
			"claude http entry by url keeps headers",
			"claude.json",
			`{"mcpServers":{"s":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${GH}"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${GH}"})},
		},
		{
			"claude sse type is http", "claude.json",
			`{"mcpServers":{"s":{"type":"sse","url":"https://sse.example.com"}}}`,
			Server{Config: remote("s", "https://sse.example.com", nil)},
		},
		{
			"claude ${VAR} in args is kept in the agent",
			"claude.json",
			`{"mcpServers":{"s":{"command":"run","args":["--root","${HOME}/src"]}}}`,
			Server{
				Config:           stdio("s", "run", "--root", "${HOME}/src"),
				UnexpandableRefs: []string{"an environment variable in command or args"},
			},
		},
		{
			"claude $ in args is literal, as Claude Code passes it", "claude.json",
			`{"mcpServers":{"s":{"command":"grep","args":["^end$"]}}}`,
			Server{Config: stdio("s", "grep", "^end$")},
		},
		{
			"claude default-value syntax is kept in the agent",
			"claude.json",
			`{"mcpServers":{"s":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${GH:-none}"}}}}`,
			Server{
				Config: remote(
					"s",
					"https://example.com/mcp",
					map[string]string{"Authorization": "Bearer ${GH:-none}"},
				),
				UnexpandableRefs: []string{"an environment variable syntax mini doesn't read"},
			},
		},
		{
			"cursor ${env:VAR} becomes ${VAR}",
			"mcp.json",
			`{"mcpServers":{"s":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${env:API_KEY}"}}}}`,
			Server{
				Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${API_KEY}"}),
			},
		},
		{
			"cursor envFile is ignored and named", "mcp.json",
			`{"mcpServers":{"s":{"command":"run","envFile":".env"}}}`,
			Server{Config: stdio("s", "run"), IgnoredRunSettings: []string{"envFile"}},
		},
		{
			"windsurf serverUrl is the url", "mcp_config.json",
			`{"mcpServers":{"s":{"serverUrl":"https://example.com/mcp"}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil)},
		},
		{
			"windsurf disabled and disabledTools", "mcp_config.json",
			`{"mcpServers":{"s":{"command":"run","disabled":true,"disabledTools":["delete"]}}}`,
			Server{Config: stdio("s", "run"), Disabled: true},
		},
		{
			"claude oauth client is dropped and named", "claude.json",
			`{"mcpServers":{"s":{"type":"http","url":"https://example.com/mcp","oauth":{"clientId":"synthetic"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil), IgnoredRunSettings: []string{"oauth"}},
		},
		{
			"cursor editor placeholders are kept in the agent",
			"mcp.json",
			`{"mcpServers":{"s":{"command":"run","env":{"ROOT":"${workspaceFolder}/data"}}}}`,
			Server{
				Config: config.ServerConfig{
					Name:    "s",
					Command: "run",
					Env:     []string{"ROOT=${workspaceFolder}/data"},
				},
				UnexpandableRefs: []string{"an editor placeholder like ${userHome}"},
			},
		},
	}
	runReaderCases(t, ReadClaude, tests)
}

func TestReadClaude_projectServersAreLeftToTheirProjects(t *testing.T) {
	path := writeClientConfig(t, "claude.json", `{
		"mcpServers":{"user":{"command":"run"}},
		"projects":{"/home/user/proj":{"mcpServers":{"project":{"command":"run"}}}}
	}`)
	got, err := ReadClaude(path)
	if err != nil || !reflect.DeepEqual(slices.Sorted(maps.Keys(got)), []string{"user"}) {
		t.Fatalf("ReadClaude = %v, %v; want only the user-scoped server", got, err)
	}
	onlyProjects := writeClientConfig(
		t,
		"claude.json",
		`{"projects":{"/home/user/proj":{"mcpServers":{"project":{"command":"run"}}}}}`,
	)
	if got, err := ReadClaude(onlyProjects); err != nil || len(got) != 0 {
		t.Fatalf("ReadClaude with only project servers = %v, %v; want nothing", got, err)
	}
}

func TestReadClaude_missingFileIsAnError(t *testing.T) {
	if _, err := ReadClaude(filepath.Join(tempDir(t), "missing.json")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestReadClaude_aMalformedEntryIsAnError(t *testing.T) {
	if _, err := ReadClaude(
		writeClientConfig(t, "claude.json", `{"mcpServers":{"s":{"args":"not a list"}}}`),
	); err == nil {
		t.Fatal("expected a parse error")
	}
}

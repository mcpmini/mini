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
	tests := []struct {
		name   string
		read   readerFunc
		file   string
		config string
		want   Server
	}{
		{
			"claude desktop stdio entry, env as a sorted KEY=VALUE list",
			ReadClaude,
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
			ReadClaude,
			"claude.json",
			`{"mcpServers":{"s":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${GH}"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${GH}"})},
		},
		{
			"claude sse type is http", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"type":"sse","url":"https://sse.example.com"}}}`,
			Server{Config: remote("s", "https://sse.example.com", nil)},
		},
		{
			"claude ${VAR} in args is kept in the agent",
			ReadClaude,
			"claude.json",
			`{"mcpServers":{"s":{"command":"run","args":["--root","${HOME}/src"]}}}`,
			Server{
				Config:           stdio("s", "run", "--root", "${HOME}/src"),
				UnexpandableRefs: []string{"an environment variable in command or args"},
			},
		},
		{
			"claude $ in args is literal, as Claude Code passes it", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"command":"grep","args":["^end$"]}}}`,
			Server{Config: stdio("s", "grep", "^end$")},
		},
		{
			"claude default-value syntax is kept in the agent",
			ReadClaude,
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
			ReadClaude,
			"mcp.json",
			`{"mcpServers":{"s":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${env:API_KEY}"}}}}`,
			Server{
				Config: remote("s", "https://example.com/mcp", map[string]string{"Authorization": "Bearer ${API_KEY}"}),
			},
		},
		{
			"cursor envFile is ignored and named", ReadClaude, "mcp.json",
			`{"mcpServers":{"s":{"command":"run","envFile":".env"}}}`,
			Server{Config: stdio("s", "run"), IgnoredRunSettings: []string{"envFile"}},
		},
		{
			"windsurf serverUrl is the url", ReadClaude, "mcp_config.json",
			`{"mcpServers":{"s":{"serverUrl":"https://example.com/mcp"}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil)},
		},
		{
			"windsurf disabled and disabledTools", ReadClaude, "mcp_config.json",
			`{"mcpServers":{"s":{"command":"run","disabled":true,"disabledTools":["delete"]}}}`,
			Server{Config: stdio("s", "run"), Disabled: true},
		},
		{
			"claude oauth client is dropped and named", ReadClaude, "claude.json",
			`{"mcpServers":{"s":{"type":"http","url":"https://example.com/mcp","oauth":{"clientId":"synthetic"}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil), IgnoredRunSettings: []string{"oauth"}},
		},
		{
			"cursor editor placeholders are kept in the agent",
			ReadClaude,
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

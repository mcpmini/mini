package agents

import "testing"

func TestReadAnyConfig_readsEachAgentsFormat(t *testing.T) {
	runReaderCases(t, ReadAnyConfig, []readerCase{
		{
			name: "mcpServers JSON", file: "team.json",
			config: `{"mcpServers":{"s":{"command":"npx"}}}`,
			want:   Server{Config: stdio("s", "npx")},
		},
		{
			name: "OpenClaw JSON", file: "openclaw.json",
			config: `{"mcp":{"servers":{"s":{"command":"npx"}}}}`,
			want:   Server{Config: stdio("s", "npx")},
		},
		{
			name: "Codex TOML", file: "team.toml",
			config: "[mcp_servers.s]\ncommand = \"npx\"\n",
			want:   Server{Config: stdio("s", "npx")},
		},
	})
}

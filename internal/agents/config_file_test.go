package agents

import (
	"strings"
	"testing"
)

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
			name: "mcpServers JSON with an unrelated mcp key", file: "team.json",
			config: `{"mcp":true,"mcpServers":{"s":{"command":"npx"}}}`,
			want:   Server{Config: stdio("s", "npx")},
		},
		{
			name: "Codex TOML", file: "team.toml",
			config: "[mcp_servers.s]\ncommand = \"npx\"\n",
			want:   Server{Config: stdio("s", "npx")},
		},
	})
}

func TestReadAnyConfig_aFileInAnotherFormatIsAnError(t *testing.T) {
	for name, config := range map[string]string{
		"VS Code":  `{"servers":{"gh":{"type":"http","url":"https://gh.example/mcp"}}}`,
		"opencode": `{"mcp":{"gh":{"type":"remote","url":"https://gh.example/mcp"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadAnyConfig(writeClientConfig(t, "mcp.json", config))
			if err == nil || !strings.Contains(err.Error(), "no mcpServers key") {
				t.Errorf("err = %v, want it to name the keys mini reads", err)
			}
		})
	}
}

func TestReadAnyConfig_anEmptyConfigReadsAsNoMCPs(t *testing.T) {
	servers, err := ReadAnyConfig(writeClientConfig(t, "team.json", `{"mcpServers":{}}`))
	if err != nil || len(servers) != 0 {
		t.Errorf("servers, err = %v, %v; want none and no error", servers, err)
	}
}

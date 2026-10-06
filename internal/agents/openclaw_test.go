package agents

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestOpenClawReader(t *testing.T) {
	tests := []readerCase{
		{
			name: "openclaw cwd and tls settings are dropped and named", file: "openclaw.json",
			config: `{"mcp":{"servers":{"s":{"command":"npx","cwd":"/srv","sslVerify":false}}}}`,
			want:   Server{Config: stdio("s", "npx"), IgnoredRunSettings: []string{"cwd", "sslVerify"}},
		},
		{
			name: "openclaw stdio entry", file: "openclaw.json",
			config: `{"mcp":{"servers":{"s":{"command":"npx","env":{"ROOT":"/data"}}}}}`,
			want:   Server{Config: config.ServerConfig{Name: "s", Command: "npx", Env: []string{"ROOT=/data"}}},
		},
		{
			name:   "openclaw tool filter and approval settings are dropped",
			file:   "openclaw.json",
			config: `{"mcp":{"servers":{"s":{"command":"npx","toolFilter":{"allow":["read"]},"codex":{"approval":"prompt"}}}}}`,
			want:   Server{Config: stdio("s", "npx")},
		},
		{
			name: "openclaw http entry switched off", file: "openclaw.json",
			config: `{"mcp":{"servers":{"s":{"url":"https://example.com/mcp","enabled":false}}}}`,
			want:   Server{Config: remote("s", "https://example.com/mcp", nil), Disabled: true},
		},
	}
	runReaderCases(t, ReadOpenClaw, tests)
}

func TestReadOpenClaw_unparsableConfigIsAnError(t *testing.T) {
	if _, err := ReadOpenClaw(writeClientConfig(t, "openclaw.json", "{not valid")); err == nil {
		t.Fatal("expected a parse error")
	}
}

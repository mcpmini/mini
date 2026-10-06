package agents

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestOpenClawReader(t *testing.T) {
	tests := []readerCase{
		{
			"openclaw cwd and tls settings are dropped and named", "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","cwd":"/srv","sslVerify":false}}}}`,
			Server{Config: stdio("s", "npx"), IgnoredRunSettings: []string{"cwd", "sslVerify"}},
		},
		{
			"openclaw stdio entry", "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","env":{"ROOT":"/data"}}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "npx", Env: []string{"ROOT=/data"}}},
		},
		{
			"openclaw tool filter and approval settings are dropped", "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","toolFilter":{"allow":["read"]},"codex":{"approval":"prompt"}}}}}`,
			Server{Config: stdio("s", "npx")},
		},
		{
			"openclaw http entry switched off", "openclaw.json",
			`{"mcp":{"servers":{"s":{"url":"https://example.com/mcp","enabled":false}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil), Disabled: true},
		},
	}
	runReaderCases(t, ReadOpenClaw, tests)
}

func TestReadOpenClaw_unparsableConfigIsAnError(t *testing.T) {
	if _, err := ReadOpenClaw(writeClientConfig(t, "openclaw.json", "{not valid")); err == nil {
		t.Fatal("expected a parse error")
	}
}

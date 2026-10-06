package agents

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestOpenClawReader(t *testing.T) {
	tests := []struct {
		name   string
		read   readerFunc
		file   string
		config string
		want   Server
	}{
		{
			"openclaw cwd and tls settings are dropped and named", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","cwd":"/srv","sslVerify":false}}}}`,
			Server{Config: stdio("s", "npx"), IgnoredRunSettings: []string{"cwd", "sslVerify"}},
		},
		{
			"openclaw stdio entry", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","env":{"ROOT":"/data"}}}}}`,
			Server{Config: config.ServerConfig{Name: "s", Command: "npx", Env: []string{"ROOT=/data"}}},
		},
		{
			"openclaw tool filter and approval settings are dropped", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"command":"npx","toolFilter":{"allow":["read"]},"codex":{"approval":"prompt"}}}}}`,
			Server{Config: stdio("s", "npx")},
		},
		{
			"openclaw http entry switched off", ReadOpenClaw, "openclaw.json",
			`{"mcp":{"servers":{"s":{"url":"https://example.com/mcp","enabled":false}}}}`,
			Server{Config: remote("s", "https://example.com/mcp", nil), Disabled: true},
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

func TestReadOpenClaw_unparsableConfigIsAnError(t *testing.T) {
	if _, err := ReadOpenClaw(writeClientConfig(t, "openclaw.json", "{not valid")); err == nil {
		t.Fatal("expected a parse error")
	}
}

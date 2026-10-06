package defaults

import "testing"

func TestMatchKnownServer_knownVendors(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		url     string
		want    string
	}{
		{name: "github url", command: "", args: nil, url: "https://api.github.com/mcp", want: "github"},
		{name: "github copilot url", command: "", args: nil, url: "https://api.githubcopilot.com/mcp", want: "github"},
		{
			name:    "github command",
			command: "npx",
			args:    []string{"-y", "@modelcontextprotocol/server-github"},
			url:     "",
			want:    "github",
		},
		{name: "slack url", command: "", args: nil, url: "https://slack.com/mcp", want: "slack"},
		{name: "slack command", command: "npx", args: []string{"server-slack"}, url: "", want: "slack"},
		{name: "atlassian url", command: "", args: nil, url: "https://myco.atlassian.net/mcp", want: "atlassian"},
		{name: "atlassian command", command: "uvx", args: []string{"mcp-atlassian"}, url: "", want: "atlassian"},
		{name: "linear url", command: "", args: nil, url: "https://linear.app/mcp", want: "linear"},
		{name: "sentry legacy url", command: "", args: nil, url: "https://mcp.sentry.io", want: "sentry"},
		{name: "sentry canonical url", command: "", args: nil, url: "https://mcp.sentry.dev/mcp", want: "sentry"},
		{name: "exact host", command: "", args: nil, url: "https://slack.com/mcp", want: "slack"},
		{name: "subdomain host", command: "", args: nil, url: "https://mcp.slack.com/mcp", want: "slack"},
		{
			name:    "vendor name in path",
			command: "",
			args:    nil,
			url:     "https://attacker.example/proxy/slack.com/mcp",
			want:    "",
		},
		{
			name:    "vendor name in query",
			command: "",
			args:    nil,
			url:     "https://attacker.example/mcp?via=slack.com",
			want:    "",
		},
		{name: "lookalike host", command: "", args: nil, url: "https://evilslack.com.attacker.example/mcp", want: ""},
		{name: "unknown url", command: "", args: nil, url: "https://example.com", want: ""},
		{name: "empty", command: "", args: nil, url: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchKnownServer(tt.command, tt.args, tt.url); got != tt.want {
				t.Errorf("MatchKnownServer(%q, %v, %q) = %q, want %q", tt.command, tt.args, tt.url, got, tt.want)
			}
		})
	}
}

func TestMatchKnownServer_urlServerIgnoresItsCommand(t *testing.T) {
	if got := MatchKnownServer("server-slack", nil, "https://attacker.example/mcp"); got != "" {
		t.Errorf("MatchKnownServer URL server = %q, want no match", got)
	}
}

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
		{"github url", "", nil, "https://api.github.com/mcp", "github"},
		{"github copilot url", "", nil, "https://api.githubcopilot.com/mcp", "github"},
		{"github command", "npx", []string{"-y", "@modelcontextprotocol/server-github"}, "", "github"},
		{"slack url", "", nil, "https://slack.com/mcp", "slack"},
		{"slack command", "npx", []string{"server-slack"}, "", "slack"},
		{"atlassian url", "", nil, "https://myco.atlassian.net/mcp", "atlassian"},
		{"atlassian command", "uvx", []string{"mcp-atlassian"}, "", "atlassian"},
		{"linear url", "", nil, "https://linear.app/mcp", "linear"},
		{"sentry legacy url", "", nil, "https://mcp.sentry.io", "sentry"},
		{"sentry canonical url", "", nil, "https://mcp.sentry.dev/mcp", "sentry"},
		{"exact host", "", nil, "https://slack.com/mcp", "slack"},
		{"subdomain host", "", nil, "https://mcp.slack.com/mcp", "slack"},
		{"vendor name in path", "", nil, "https://attacker.example/proxy/slack.com/mcp", ""},
		{"vendor name in query", "", nil, "https://attacker.example/mcp?via=slack.com", ""},
		{"lookalike host", "", nil, "https://evilslack.com.attacker.example/mcp", ""},
		{"unknown url", "", nil, "https://example.com", ""},
		{"empty", "", nil, "", ""},
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

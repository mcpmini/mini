package defaults

import "testing"

func TestDetectKey_hostMatchesExactOrSubdomain(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"exact host", "https://slack.com/mcp", "slack"},
		{"subdomain host", "https://mcp.slack.com/mcp", "slack"},
		{"unrelated host", "https://example.com/mcp", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectKey("", tt.url); got != tt.want {
				t.Errorf("DetectKey(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestDetectKey_rejectsSubstringAttacks(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"vendor name in path", "https://attacker.example/proxy/slack.com/mcp"},
		{"vendor name in query", "https://attacker.example/mcp?via=slack.com"},
		{"lookalike host", "https://evilslack.com.attacker.example/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectKey("", tt.url); got != "" {
				t.Errorf("DetectKey(%q) = %q, want no match — vendor string appears outside the host", tt.url, got)
			}
		})
	}
}

func TestDetectKey_cmdLineStillMatchesBySubstring(t *testing.T) {
	if got := DetectKey("npx -y server-slack", ""); got != "slack" {
		t.Errorf("DetectKey cmd match = %q, want slack", got)
	}
}

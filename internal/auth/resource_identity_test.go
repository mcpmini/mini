//go:build test

package auth

import "testing"

func TestPRMResourceContainsServer(t *testing.T) {
	cases := []struct {
		name, resource, server string
		allowed                bool
	}{
		{
			name:     "Slack root audience",
			resource: "https://mcp.example.test",
			server:   "https://mcp.example.test/mcp",
			allowed:  true,
		},
		{
			name:     "uppercase origin",
			resource: "HTTPS://MCP.EXAMPLE.TEST/mcp",
			server:   "https://mcp.example.test/mcp",
			allowed:  true,
		},
		{
			name:     "default HTTPS port",
			resource: "https://mcp.example.test/mcp",
			server:   "https://mcp.example.test:443/mcp",
			allowed:  true,
		},
		{
			name:     "default HTTP port",
			resource: "http://mcp.example.test:80/api",
			server:   "http://mcp.example.test/api/mcp",
			allowed:  true,
		},
		{name: "root slash", resource: "https://mcp.example.test/", server: "https://mcp.example.test", allowed: true},
		{
			name:     "server trailing slash",
			resource: "https://mcp.example.test/mcp",
			server:   "https://mcp.example.test/mcp/",
			allowed:  true,
		},
		{
			name:     "parent path",
			resource: "https://mcp.example.test/api",
			server:   "https://mcp.example.test/api/mcp",
			allowed:  true,
		},
		{
			name:     "other host",
			resource: "https://other.example.test",
			server:   "https://mcp.example.test/mcp",
			allowed:  false,
		},
		{
			name:     "other port",
			resource: "https://mcp.example.test:8443",
			server:   "https://mcp.example.test/mcp",
			allowed:  false,
		},
		{
			name:     "other scheme",
			resource: "http://mcp.example.test",
			server:   "https://mcp.example.test/mcp",
			allowed:  false,
		},
		{
			name:     "sibling prefix",
			resource: "https://mcp.example.test/api",
			server:   "https://mcp.example.test/apix",
			allowed:  false,
		},
		{
			name:     "deeper resource",
			resource: "https://mcp.example.test/api/mcp",
			server:   "https://mcp.example.test/api",
			allowed:  false,
		},
		{
			name:     "encoded separator",
			resource: "https://mcp.example.test/api",
			server:   "https://mcp.example.test/api%2Fmcp",
			allowed:  false,
		},
		{
			name:     "dot traversal",
			resource: "https://mcp.example.test/api",
			server:   "https://mcp.example.test/api/../other",
			allowed:  false,
		},
		{
			name:     "encoded dot traversal",
			resource: "https://mcp.example.test/api",
			server:   "https://mcp.example.test/api/%2e%2e/other",
			allowed:  false,
		},
		{
			name:     "userinfo",
			resource: "https://user@mcp.example.test",
			server:   "https://mcp.example.test/mcp",
			allowed:  false,
		},
		{
			name:     "fragment",
			resource: "https://mcp.example.test/#x",
			server:   "https://mcp.example.test/mcp",
			allowed:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePRMResource(tc.resource, tc.server); (err == nil) != tc.allowed {
				t.Fatalf("validatePRMResource(%q,%q) = %v; allowed=%v", tc.resource, tc.server, err, tc.allowed)
			}
		})
	}
}

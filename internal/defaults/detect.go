package defaults

import (
	"net/url"
	"strings"
)

type ServerMatcher struct {
	Key      string
	URLParts []string
	CmdParts []string
}

var KnownServers = []ServerMatcher{
	{Key: "github", URLParts: []string{"github.com", "githubcopilot.com"}, CmdParts: []string{"server-github"}},
	{Key: "slack", URLParts: []string{"slack.com"}, CmdParts: []string{"server-slack", "slack-mcp"}},
	{Key: "atlassian", URLParts: []string{"atlassian.net", "atlassian.com", "jira.com"}, CmdParts: []string{"mcp-atlassian", "server-jira", "confluence-mcp"}},
	{Key: "linear", URLParts: []string{"linear.app"}, CmdParts: []string{"server-linear", "linear-mcp"}},
	{Key: "sentry", URLParts: []string{"sentry.io", "sentry.dev"}, CmdParts: []string{"server-sentry"}},
}

// DetectKey identifies a known upstream by its URL host or command line — never by the
// user-chosen server name, since a server named e.g. "slack" pointing elsewhere must not
// receive Slack's bundled OAuth client credentials. Host matching (exact or subdomain)
// prevents a lookalike host or a path/query substring from being misidentified as the vendor.
func DetectKey(cmdLine, rawURL string) string {
	host := hostname(rawURL)
	for _, m := range KnownServers {
		if matchesHost(host, m.URLParts) || containsAny(cmdLine, m.CmdParts) {
			return m.Key
		}
	}
	return ""
}

func hostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func matchesHost(host string, parts []string) bool {
	for _, p := range parts {
		if host == p || strings.HasSuffix(host, "."+p) {
			return true
		}
	}
	return false
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

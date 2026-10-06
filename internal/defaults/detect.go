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
	{
		Key:      "atlassian",
		URLParts: []string{"atlassian.net", "atlassian.com", "jira.com"},
		CmdParts: []string{"mcp-atlassian", "server-jira", "confluence-mcp"},
	},
	{Key: "linear", URLParts: []string{"linear.app"}, CmdParts: []string{"server-linear", "linear-mcp"}},
	{Key: "sentry", URLParts: []string{"sentry.io", "sentry.dev"}, CmdParts: []string{"server-sentry"}},
}

// MatchKnownServer returns the KnownServers key for a server, or "". It never looks at the
// user-chosen server name: a server named "slack" pointing elsewhere must not get Slack's
// bundled OAuth client. A URL server is matched by host alone, since its command never runs.
func MatchKnownServer(command string, args []string, rawURL string) string {
	if rawURL != "" {
		return matchByHost(hostname(rawURL))
	}
	return matchByCommand(strings.ToLower(command + " " + strings.Join(args, " ")))
}

func matchByHost(host string) string {
	for _, m := range KnownServers {
		if matchesHost(host, m.URLParts) {
			return m.Key
		}
	}
	return ""
}

func matchByCommand(commandLine string) string {
	for _, m := range KnownServers {
		if containsAny(commandLine, m.CmdParts) {
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

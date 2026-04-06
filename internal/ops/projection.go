package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

type serverMatcher struct {
	projection string
	urlParts   []string
	cmdParts   []string
}

var knownServers = []serverMatcher{
	{projection: "github", urlParts: []string{"github.com", "githubcopilot.com"}, cmdParts: []string{"server-github"}},
	{projection: "slack", urlParts: []string{"slack.com"}, cmdParts: []string{"server-slack"}},
	{projection: "jira", urlParts: []string{"atlassian.net", "jira.com"}, cmdParts: []string{"server-jira"}},
	{projection: "linear", urlParts: []string{"linear.app"}, cmdParts: []string{"server-linear"}},
	{projection: "sentry", urlParts: []string{"sentry.io"}, cmdParts: []string{"server-sentry"}},
}

func DetectProjectionKey(sc config.ServerConfig) string {
	cmdLine := strings.ToLower(sc.Command + " " + strings.Join(sc.Args, " "))
	urlLower := strings.ToLower(sc.URL)
	for _, m := range knownServers {
		if containsAny(urlLower, m.urlParts) || containsAny(cmdLine, m.cmdParts) {
			return m.projection
		}
	}
	return ""
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func InstallBundledProjection(configDir string, sc config.ServerConfig) {
	key := DetectProjectionKey(sc)
	if key == "" {
		return
	}
	bundled := defaults.ProjectionFor(key)
	if bundled == nil {
		return
	}
	projDir := filepath.Join(configDir, "projections")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		return
	}
	dest := filepath.Join(projDir, sc.Name+".yaml")
	if _, err := os.Stat(dest); err == nil {
		return
	}
	if err := os.WriteFile(dest, bundled, 0600); err != nil {
		return
	}
	fmt.Printf("installed default projection → %s\n", dest)
}

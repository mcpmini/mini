package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

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

// installBundledPermissions applies default hidden/protected tools for known
// servers, but only when the user has not already set explicit permissions.
// This lets users override by passing --protected/--hidden flags to mini add.
func installBundledPermissions(configDir string, sc config.ServerConfig) {
	if sc.Permissions != nil {
		return
	}
	perms := loadBundledPermissions(sc)
	if perms == nil {
		return
	}
	if err := patchServerPermissions(configDir, sc.Name, perms); err == nil {
		fmt.Printf("applied default permissions → %s\n", filepath.Join(configDir, "servers", sc.Name+".yaml"))
	}
}

func loadBundledPermissions(sc config.ServerConfig) *config.PermissionsConfig {
	key := DetectProjectionKey(sc)
	if key == "" {
		return nil
	}
	raw := defaults.PermissionsFor(key)
	if raw == nil {
		return nil
	}
	var perms config.PermissionsConfig
	if err := yaml.Unmarshal(raw, &perms); err != nil {
		return nil
	}
	if len(perms.Hidden) == 0 && len(perms.Protected) == 0 {
		return nil
	}
	return &perms
}

func patchServerPermissions(configDir, name string, perms *config.PermissionsConfig) error {
	serverPath := filepath.Join(configDir, "servers", name+".yaml")
	data, err := os.ReadFile(serverPath)
	if err != nil {
		return err
	}
	var existing config.ServerConfig
	if err := yaml.Unmarshal(data, &existing); err != nil {
		return err
	}
	existing.Permissions = perms
	updated, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	return os.WriteFile(serverPath, updated, 0600)
}

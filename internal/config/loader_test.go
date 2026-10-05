package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func mustLoadOneServer(t *testing.T, dir string) config.ServerConfig {
	t.Helper()
	_, servers := mustLoadConfig(t, dir)
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	return servers[0]
}

func mustLoadOneAction(t *testing.T, dir string) config.ActionConfig {
	t.Helper()
	actions, err := config.LoadActions(dir)
	if err != nil {
		t.Fatalf("LoadActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	return actions[0]
}

func mustLoadServers(t *testing.T, dir string) config.Servers {
	t.Helper()
	servers, err := config.LoadServers(dir)
	if err != nil {
		t.Fatalf("LoadServers: %v", err)
	}
	return servers
}

func mustLoadConfig(t *testing.T, dir string) (*config.Config, []config.ServerConfig) {
	t.Helper()
	cfg, err := config.LoadMain(dir)
	if err != nil {
		t.Fatalf("LoadMain: %v", err)
	}
	servers := mustLoadServers(t, dir)
	if len(servers.Broken) > 0 {
		t.Fatalf("LoadServers: broken %+v", servers.Broken)
	}
	return cfg, servers.Loaded
}

func expectMainLoadError(t *testing.T, dir string) {
	t.Helper()
	if _, err := config.LoadMain(dir); err == nil {
		t.Fatal("expected LoadMain error")
	}
}

func expectBrokenServer(t *testing.T, dir, name string) {
	t.Helper()
	servers := mustLoadServers(t, dir)
	if !servers.IsBroken(name) {
		t.Fatalf("LoadServers: %s isn't broken; loaded %+v", name, servers.Loaded)
	}
}

func expectLoadActionsError(t *testing.T, dir string) {
	t.Helper()
	if _, err := config.LoadActions(dir); err == nil {
		t.Fatal("expected LoadActions error")
	}
}

func assertDefaultLoadState(t *testing.T, cfg *config.Config, servers []config.ServerConfig) {
	t.Helper()
	if len(servers) != 0 {
		t.Errorf("expected no servers, got %d", len(servers))
	}
}

func assertNameValidity(t *testing.T, valid []string, invalid []string, match func(string) bool, label string) {
	t.Helper()
	for _, name := range valid {
		if !match(name) {
			t.Errorf("expected %q to be a valid %s", name, label)
		}
	}
	for _, name := range invalid {
		if match(name) {
			t.Errorf("expected %q to be an invalid %s", name, label)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, servers := mustLoadConfig(t, dir)
	assertDefaultLoadState(t, cfg, servers)
}

func TestLoadMainConfig(t *testing.T) {
	dir := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.LogLevel = "debug"
	configtest.WriteConfig(t, dir, fixtureConfig)

	cfg, _ := mustLoadConfig(t, dir)
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug, got %s", cfg.LogLevel)
	}
}

func TestLoad_serverNameComesFromFile(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "ci",
		Command: "npx",
		Args:    []string{"-y", "@buildkite/mcp-server"},
	})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "ci",
		Tools: map[string]*config.ProjectionConfig{
			"list_builds": {
				IncludeOnly: []string{"id"},
			},
		},
	})
	_, servers := mustLoadConfig(t, dir)
	if len(servers) != 1 || servers[0].Name != "ci" {
		t.Fatalf("servers = %#v, want one named ci", servers)
	}
	if p := servers[0].Projections["list_builds"]; p == nil || len(p.IncludeOnly) != 1 {
		t.Errorf("projections = %v, want inline projections loaded", servers[0].Projections)
	}
}

func TestLoad_invalidServerFileName(t *testing.T) {
	for _, file := range []string{".yaml", "bad name!.yaml", "a.b.yaml"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, filepath.Join(dir, "servers", file), "command: echo\n")
			servers := mustLoadServers(t, dir)
			if len(servers.Broken) != 1 || !strings.Contains(servers.Broken[0].Err.Error(), "invalid server name") {
				t.Fatalf("Broken = %+v, want an invalid server name error", servers.Broken)
			}
		})
	}
}

func TestLoadMalformedMainConfig(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), `not: valid: yaml: [`)
	expectMainLoadError(t, dir)
}

func TestLoadMalformedServerConfig(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "bad.yaml"), `not: valid: yaml: [`)
	expectBrokenServer(t, dir, "bad")
}

func TestLoadMissingConfigDir_usesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, servers := mustLoadConfig(t, dir)
	assertDefaultLoadState(t, cfg, servers)
}

func TestLoadProjectionConfig(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "gh-mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "gh",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				IncludeOnly: []string{"number", "title"},
				ArrayLimits: map[string]int{"labels": 3},
			},
		},
	})
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections
	if proj == nil {
		t.Fatal("expected projections to be loaded")
	}
	if proj["list_issues"] == nil {
		t.Fatal("expected list_issues projection")
	}
	if len(proj["list_issues"].IncludeOnly) != 2 {
		t.Errorf("expected 2 include_only fields, got %v", proj["list_issues"].IncludeOnly)
	}
}

func TestLoadProjectionMerges_dirWinsOverInline(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "svc",
		Command: "my-mcp",
		Projections: map[string]*config.ProjectionConfig{
			"my_tool": {
				IncludeOnly: []string{"inline_field"},
			},
		},
	})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"my_tool": {
				IncludeOnly: []string{"dir_field"},
			},
		},
	})
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections["my_tool"]
	if proj == nil {
		t.Fatal("expected projection")
		return
	}
	if len(proj.IncludeOnly) != 1 || proj.IncludeOnly[0] != "dir_field" {
		t.Errorf("expected dir projection to win, got include_only=%v", proj.IncludeOnly)
	}
}

func TestLoadActions_basic(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteAction(t, dir, config.ActionConfig{
		Name:        "my_prs",
		Description: "My open PRs",
		Server:      "gh",
		Tool:        "list_pull_requests",
		DefaultArgs: map[string]any{"author": "@me", "state": "open"},
	})
	ac := mustLoadOneAction(t, dir)
	assertActionDefaults(t, ac, "my_prs", "state", "open")
}

func assertActionDefaults(t *testing.T, ac config.ActionConfig, wantName, wantKey string, wantValue any) {
	t.Helper()
	if ac.Name != wantName {
		t.Errorf("expected name=%s, got %q", wantName, ac.Name)
	}
	if ac.DefaultArgs[wantKey] != wantValue {
		t.Errorf("expected %s=%v in default_args, got %v", wantKey, wantValue, ac.DefaultArgs[wantKey])
	}
}

func TestLoadActions_nameFromFilename(t *testing.T) {
	dir := t.TempDir()
	// action file with no name field → name derived from filename
	testutil.WriteFile(t, filepath.Join(dir, "internal", "actions", "my_action.yaml"), `
server: gh
tool: list_issues
`)
	ac := mustLoadOneAction(t, dir)
	if ac.Name != "my_action" {
		t.Errorf("expected name from filename, got %q", ac.Name)
	}
}

func TestLoadActions_emptyDir(t *testing.T) {
	dir := t.TempDir()
	actions, err := config.LoadActions(dir)
	if err != nil {
		t.Fatalf("unexpected error for empty dir: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
}

func TestValidServerName(t *testing.T) {
	assertNameValidity(
		t,
		[]string{"myserver", "my-server", "my_server", "MyServer123", "a", "A1_B-2"},
		[]string{"", "my server", "my.server", "my/server", "my@server", "server!", "../etc"},
		config.ValidServerName.MatchString,
		"server name",
	)
}

func TestValidToolName(t *testing.T) {
	assertNameValidity(
		t,
		[]string{"list_issues", "get-file", "read.resource", "tool123", "a"},
		[]string{"", "my tool", "tool/name", "tool@name", "tool name!"},
		config.ValidToolName.MatchString,
		"tool name",
	)
}

func TestLoadServerConfig_handshakeTimeoutParses(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "ci", Command: "mcp", HandshakeTimeout: "3s"})
	sc := mustLoadOneServer(t, dir)
	if sc.HandshakeTimeout != "3s" {
		t.Fatalf("expected handshake_timeout %q, got %q", "3s", sc.HandshakeTimeout)
	}
}

func TestLoad_invalidHandshakeTimeout(t *testing.T) {
	for _, spec := range []string{"-1s", "nonsense"} {
		t.Run(spec, func(t *testing.T) {
			dir := t.TempDir()
			configtest.WriteServer(t, dir, config.ServerConfig{Name: "ci", Command: "mcp", HandshakeTimeout: spec})
			expectBrokenServer(t, dir, "ci")
		})
	}
}

func TestLoadServerConfig_withPermissions(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "ci",
		Command: "mcp-ci",
		Permissions: &config.PermissionsConfig{
			Default:   "open",
			Protected: []string{"deleteProject", "clearCache"},
			Hidden:    []string{"internalDebug"},
		},
	})
	sc := mustLoadOneServer(t, dir)
	assertPermissions(t, sc, 2, []string{"internalDebug"})
}

func assertPermissions(t *testing.T, sc config.ServerConfig, wantProtected int, wantHidden []string) {
	t.Helper()
	if sc.Permissions == nil {
		t.Fatal("expected permissions to be loaded")
	}
	perm := sc.Permissions
	if len(perm.Protected) != wantProtected {
		t.Errorf("expected %d protected tools, got %v", wantProtected, perm.Protected)
	}
	if len(perm.Hidden) != len(wantHidden) || perm.Hidden[0] != wantHidden[0] {
		t.Errorf("expected hidden=%v, got %v", wantHidden, perm.Hidden)
	}
}

func TestLoadResponseFormat(t *testing.T) {
	t.Run("toon accepted", func(t *testing.T) {
		dir := t.TempDir()
		fixtureConfig := config.DefaultConfig()
		fixtureConfig.ResponseFormat = "toon"
		configtest.WriteConfig(t, dir, fixtureConfig)

		cfg, _ := mustLoadConfig(t, dir)
		if cfg.ResponseFormat != "toon" {
			t.Errorf("expected toon, got %q", cfg.ResponseFormat)
		}
	})
	t.Run("mini rejected naming toon as the replacement", func(t *testing.T) {
		dir := t.TempDir()
		fixtureConfig := config.DefaultConfig()
		fixtureConfig.ResponseFormat = "mini"
		configtest.WriteConfig(t, dir, fixtureConfig)

		_, err := config.LoadMain(dir)
		if err == nil || !strings.Contains(err.Error(), "toon") {
			t.Fatalf("expected error naming toon as the replacement, got %v", err)
		}
	})
	t.Run("unknown format rejected", func(t *testing.T) {
		dir := t.TempDir()
		fixtureConfig := config.DefaultConfig()
		fixtureConfig.ResponseFormat = "xml"
		configtest.WriteConfig(t, dir, fixtureConfig)

		expectMainLoadError(t, dir)
	})
}

func TestLoadProjectionFormat_rejectsMini(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "gh-mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "gh",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				Format: "mini",
			},
		},
	})
	sc, err := config.LoadServer(dir, "gh")
	if err != nil || sc.ProjectionsErr == nil || !strings.Contains(sc.ProjectionsErr.Err.Error(), "toon") {
		t.Fatalf("LoadServer = %+v, %v; want a projection format error naming toon", sc.ProjectionsErr, err)
	}
}

func TestValidResponseFormat(t *testing.T) {
	for _, format := range []string{"", "json", "toon"} {
		if err := config.ValidResponseFormat(format); err != nil {
			t.Errorf("ValidResponseFormat(%q) = %v, want nil", format, err)
		}
	}
	if err := config.ValidResponseFormat("mini"); err == nil || !strings.Contains(err.Error(), "toon") {
		t.Errorf("ValidResponseFormat(\"mini\") = %v, want error naming toon", err)
	}
	if err := config.ValidResponseFormat("xml"); err == nil {
		t.Error("ValidResponseFormat(\"xml\") = nil, want error")
	}
}

func TestEffectiveFormat(t *testing.T) {
	cases := []struct {
		name, explicit, projection, global, want string
	}{
		{"explicit wins over all", config.FormatToon, config.FormatJSON, "", config.FormatToon},
		{"projection when no explicit", "", config.FormatToon, config.FormatJSON, config.FormatToon},
		{"global when no explicit or projection", "", "", config.FormatToon, config.FormatToon},
		{"json default when all empty", "", "", "", config.FormatJSON},
		{"explicit json beats toon projection", config.FormatJSON, config.FormatToon, config.FormatToon, config.FormatJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := config.EffectiveFormat(tc.explicit, tc.projection, tc.global)
			if got != tc.want {
				t.Errorf("EffectiveFormat(%q, %q, %q) = %q, want %q", tc.explicit, tc.projection, tc.global, got, tc.want)
			}
		})
	}
}

func TestLoadActions_malformedYAML_returnsError(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "internal", "actions", "bad.yaml"), `not: valid: yaml: [`)
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidActionName_returnsError(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "internal", "actions", "bad.yaml"), "name: \"bad name\"\nserver: gh\ntool: list\n")
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteAction(t, dir, config.ActionConfig{Name: "act", Server: "bad server", Tool: "list"})
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidToolName_returnsError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteAction(t, dir, config.ActionConfig{Name: "act", Server: "gh", Tool: "bad/tool"})
	expectLoadActionsError(t, dir)
}

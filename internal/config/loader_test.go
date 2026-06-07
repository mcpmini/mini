package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustLoadOneServer(t *testing.T, dir string) config.ServerConfig {
	t.Helper()
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
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

func mustLoadConfig(t *testing.T, dir string) (*config.Config, []config.ServerConfig) {
	t.Helper()
	cfg, servers, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg, servers
}

func expectLoadError(t *testing.T, dir string) {
	t.Helper()
	_, _, err := config.Load(dir)
	if err == nil {
		t.Fatal("expected load error")
	}
}

func expectLoadActionsError(t *testing.T, dir string) {
	t.Helper()
	if _, err := config.LoadActions(dir); err == nil {
		t.Fatal("expected LoadActions error")
	}
}

func assertOneServerName(t *testing.T, dir, want string) {
	t.Helper()
	cfg, servers := mustLoadConfig(t, dir)
	_ = cfg
	if len(servers) != 1 || servers[0].Name != want {
		t.Fatalf("expected one server %q, got %#v", want, servers)
	}
}

func assertDefaultLoadState(t *testing.T, cfg *config.Config, servers []config.ServerConfig) {
	t.Helper()
	if cfg.InlineThreshold != 3500 {
		t.Errorf("expected default inline threshold 2000, got %d", cfg.InlineThreshold)
	}
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
	writeFile(t, filepath.Join(dir, "config.yaml"), `
log_level: debug
inline_threshold: 200
`)
	cfg, _ := mustLoadConfig(t, dir)
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug, got %s", cfg.LogLevel)
	}
	if cfg.InlineThreshold != 200 {
		t.Errorf("expected 200, got %d", cfg.InlineThreshold)
	}
}

func TestLoadServerConfigs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "ci.yaml"), `
name: ci
command: npx
args: ["-y", "@buildkite/mcp-server"]
`)
	assertOneServerName(t, dir, "ci")
}

func TestLoadInlineServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
servers:
  - name: fs
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
`)
	assertOneServerName(t, dir, "fs")
}

func TestLoadMalformedMainConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `not: valid: yaml: [`)
	expectLoadError(t, dir)
}

func TestLoadMalformedServerConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "bad.yaml"), `not: valid: yaml: [`)
	expectLoadError(t, dir)
}

func TestLoadMissingConfigDir_usesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, servers := mustLoadConfig(t, dir)
	assertDefaultLoadState(t, cfg, servers)
}

func TestLoadProjectionConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), `name: gh
command: gh-mcp`)
	writeFile(t, filepath.Join(dir, "projections", "gh.yaml"), `
list_issues:
  include: [number, title]
  array_limits:
    labels: 3
`)
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections
	if proj == nil {
		t.Fatal("expected projections to be loaded")
	}
	if proj["list_issues"] == nil {
		t.Fatal("expected list_issues projection")
	}
	if len(proj["list_issues"].Include) != 2 {
		t.Errorf("expected 2 include fields, got %v", proj["list_issues"].Include)
	}
}

func TestLoadProjectionMerges_dirWinsOverInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `name: svc
command: my-mcp
projections:
  my_tool:
    include: [inline_field]
`)
	writeFile(t, filepath.Join(dir, "projections", "svc.yaml"), `
my_tool:
  include: [dir_field]
`)
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections["my_tool"]
	if proj == nil {
		t.Fatal("expected projection")
	}
	if len(proj.Include) != 1 || proj.Include[0] != "dir_field" {
		t.Errorf("expected dir projection to win, got include=%v", proj.Include)
	}
}

func TestLoadActions_basic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "actions", "my_prs.yaml"), `
name: my_prs
description: My open PRs
server: gh
tool: list_pull_requests
default_args:
  state: open
  author: "@me"
`)
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
	writeFile(t, filepath.Join(dir, "actions", "my_action.yaml"), `
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

func TestLoad_invalidServerName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "bad name.yaml"), "name: bad name\ncommand: mcp\n")
	expectLoadError(t, dir)
}

func TestLoadServerConfig_withAuth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "notion.yaml"), `
name: notion
transport: http
url: https://mcp.notion.com
auth:
  type: oauth2
  client_id: abc123
  token_url: https://api.notion.com/v1/oauth/token
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil {
		t.Fatal("expected auth config to be loaded")
	}
	assertAuthConfig(t, sc, "oauth2", "abc123")
}

func assertAuthConfig(t *testing.T, sc config.ServerConfig, wantType, wantClientID string) {
	t.Helper()
	if sc.Auth == nil {
		t.Fatal("expected auth config to be loaded")
	}
	if sc.Auth.Type != wantType {
		t.Errorf("expected auth type %s, got %q", wantType, sc.Auth.Type)
	}
	if sc.Auth.ClientID != wantClientID {
		t.Errorf("expected client_id=%s, got %q", wantClientID, sc.Auth.ClientID)
	}
}

func TestLoadServerConfig_withPermissions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "ci.yaml"), `
name: ci
command: mcp-ci
permissions:
  default: open
  protected: [deleteProject, clearCache]
  hidden: [internalDebug]
`)
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

func TestLoad_deduplicatesDuplicateServerNames(t *testing.T) {
	dir := t.TempDir()
	// Same server name appears in servers/ dir and in config.yaml
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), "name: svc\ncommand: my-mcp\n")
	writeFile(t, filepath.Join(dir, "config.yaml"), "servers:\n  - name: svc\n    command: other-mcp\n")

	_, servers := mustLoadConfig(t, dir)
	if len(servers) != 1 {
		t.Errorf("expected 1 server after dedup, got %d", len(servers))
	}
	if servers[0].Command != "my-mcp" {
		t.Errorf("expected dir server to win (command=my-mcp), got %q", servers[0].Command)
	}
}

func TestLoadProjection_malformedYAML_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "projections", "srv.yaml"), `not: valid: yaml: [`)
	expectLoadError(t, dir)
}

func TestLoadActions_malformedYAML_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "actions", "bad.yaml"), `not: valid: yaml: [`)
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidActionName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "actions", "bad.yaml"), "name: \"bad name\"\nserver: gh\ntool: list\n")
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "actions", "act.yaml"), "name: act\nserver: \"bad server\"\ntool: list\n")
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidToolName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "actions", "act.yaml"), "name: act\nserver: gh\ntool: \"bad/tool\"\n")
	expectLoadActionsError(t, dir)
}

func TestDefaultConfig_hasExpectedValues(t *testing.T) {
	cfg := config.DefaultConfig()
	assertDefaultConfigFields(t, cfg)
}

func assertDefaultConfigFields(t *testing.T, cfg *config.Config) {
	t.Helper()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"InlineThreshold", cfg.InlineThreshold, 3500},
		{"DefaultDepthLimit", cfg.DefaultDepthLimit, 0},
		{"DefaultStringLimit", cfg.DefaultStringLimit, 0},
		{"LogLevel", cfg.LogLevel, "info"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, c.got)
		}
	}
	if len(cfg.ContentFields) == 0 {
		t.Error("expected non-empty default content fields")
	}
}

func TestServerConfig_IsEnabled(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name string
		sc   config.ServerConfig
		want bool
	}{
		{"nil enabled field defaults to true", config.ServerConfig{}, true},
		{"explicit true", config.ServerConfig{Enabled: &enabled}, true},
		{"explicit false", config.ServerConfig{Enabled: &disabled}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.IsEnabled(); got != tc.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindServer(t *testing.T) {
	servers := []config.ServerConfig{
		{Name: "alpha"},
		{Name: "beta"},
	}
	t.Run("found", func(t *testing.T) {
		got := config.FindServer(servers, "beta")
		if got == nil || got.Name != "beta" {
			t.Fatalf("FindServer returned %v, want beta", got)
		}
	})
	t.Run("not found", func(t *testing.T) {
		if got := config.FindServer(servers, "gamma"); got != nil {
			t.Fatalf("FindServer returned %v, want nil", got)
		}
	})
	t.Run("returns pointer into slice", func(t *testing.T) {
		got := config.FindServer(servers, "alpha")
		got.Name = "modified"
		if servers[0].Name != "modified" {
			t.Fatal("FindServer should return pointer into slice")
		}
	})
}

package config_test

import (
	"os"
	"path/filepath"
	"strings"
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
	writeFile(t, filepath.Join(dir, "config.yaml"), `
log_level: debug
`)
	cfg, _ := mustLoadConfig(t, dir)
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug, got %s", cfg.LogLevel)
	}
}

func TestLoad_serverNameComesFromFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "ci.yaml"), `
command: npx
args: ["-y", "@buildkite/mcp-server"]
`)
	writeFile(t, filepath.Join(dir, "servers", "ci.proj.yaml"), "list_builds:\n  include_only: [id]\n")
	_, servers := mustLoadConfig(t, dir)
	sc := config.FindServer(servers, "ci")
	if len(servers) != 1 || sc == nil {
		t.Fatalf("servers = %#v, want one named ci", servers)
	}
	if p := sc.Projections["list_builds"]; p == nil || len(p.IncludeOnly) != 1 {
		t.Errorf("projections = %v, want ci.proj.yaml applied to ci", sc.Projections)
	}
}

func TestLoad_invalidServerFileName(t *testing.T) {
	for _, file := range []string{".yaml", "bad name!.yaml", "a.b.yaml"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "servers", file), "command: echo\n")
			if _, _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), "invalid server name") {
				t.Fatalf("want an invalid server name error, got %v", err)
			}
		})
	}
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
	writeFile(t, filepath.Join(dir, "servers", "gh.proj.yaml"), `
list_issues:
  include_only: [number, title]
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
	if len(proj["list_issues"].IncludeOnly) != 2 {
		t.Errorf("expected 2 include_only fields, got %v", proj["list_issues"].IncludeOnly)
	}
}

func TestLoadProjectionMerges_dirWinsOverInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `name: svc
command: my-mcp
projections:
  my_tool:
    include_only: [inline_field]
`)
	writeFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), `
my_tool:
  include_only: [dir_field]
`)
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
	writeFile(t, filepath.Join(dir, "internal", "actions", "my_prs.yaml"), `
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
	writeFile(t, filepath.Join(dir, "internal", "actions", "my_action.yaml"), `
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
	writeFile(t, filepath.Join(dir, "servers", "ci.yaml"), "name: ci\ncommand: mcp\nhandshake_timeout: 3s\n")
	sc := mustLoadOneServer(t, dir)
	if sc.HandshakeTimeout != "3s" {
		t.Fatalf("expected handshake_timeout %q, got %q", "3s", sc.HandshakeTimeout)
	}
}

func TestLoad_invalidHandshakeTimeout(t *testing.T) {
	for _, spec := range []string{"-1s", "nonsense"} {
		t.Run(spec, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "servers", "ci.yaml"), "name: ci\ncommand: mcp\nhandshake_timeout: "+spec+"\n")
			expectLoadError(t, dir)
		})
	}
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

func TestLoadServerConfig_mergesDetectedOAuthMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "detected.yaml"), `
name: detected
transport: http
url: https://example.com/mcp
`)
	if err := config.MarkOAuthDetected(dir, "detected"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 merged in from the detected marker", sc.Auth)
	}
}

func TestLoadServerConfig_existingAuthTakesPrecedenceOverDetectedMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "hasauth.yaml"), `
name: hasauth
transport: http
url: https://example.com/mcp
auth:
  type: apikey
  token: secret
`)
	if err := config.MarkOAuthDetected(dir, "hasauth"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "apikey" {
		t.Errorf("Auth = %+v, a hand-configured auth block must never be overridden by a detected marker", sc.Auth)
	}
}

func TestLoadServerConfig_mergesBundledAuthForKnownServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "slack.yaml"), `
name: slack
transport: http
url: https://mcp.slack.com/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		t.Fatalf("Auth = %+v, want type oauth2 merged in from the slack bundled default", sc.Auth)
	}
	if sc.Auth.ClientID == "" {
		t.Error("ClientID is empty — Slack's pre-registered client_id was not merged in")
	}
	if sc.Auth.CallbackPort != 3118 {
		t.Errorf("CallbackPort = %d, want 3118", sc.Auth.CallbackPort)
	}
}

func TestLoadServerConfig_bundledAuthMatchesByURLNotName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "slack.yaml"), `
name: slack
transport: http
url: https://example.com/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a server merely named 'slack' but pointed elsewhere must not get Slack's bundled OAuth credentials", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthMatchesRenamedKnownServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "myslack.yaml"), `
name: myslack
transport: http
url: https://mcp.slack.com/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, a server pointed at slack.com should get the bundled default regardless of its chosen name", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthRejectsVendorNameInPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `
name: svc
transport: http
url: https://attacker.example/proxy/slack.com/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a vendor name appearing only in the URL path must not trigger the bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthRejectsLookalikeHost(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `
name: svc
transport: http
url: https://evilslack.com.attacker.example/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a lookalike host must not trigger the bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthIgnoresCommandOnURLServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `
name: svc
transport: http
url: https://attacker.example/mcp
command: server-slack
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a URL server command must not trigger bundled auth", sc.Auth)
	}
}

func TestLoadServerConfig_unknownServerGetsNoBundledAuth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "unknown.yaml"), `
name: unknown
transport: http
url: https://example.com/mcp
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, want nil for a server with no bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_existingAuthTakesPrecedenceOverBundledDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "slack.yaml"), `
name: slack
transport: http
url: https://mcp.slack.com/mcp
auth:
  type: apikey
  token: mytoken
`)
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "apikey" {
		t.Errorf("Auth = %+v, a hand-configured auth block must never be overridden by a bundled default", sc.Auth)
	}
}

func TestAuthConfig_HeaderName(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty defaults to Authorization", "", "Authorization"},
		{"custom header preserved", "X-Api-Key", "X-Api-Key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := config.AuthConfig{Header: tc.header}
			if got := ac.HeaderName(); got != tc.want {
				t.Errorf("HeaderName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServerConfig_HasStaticAuthHeader(t *testing.T) {
	t.Setenv("MINI_TEST_STATIC_AUTH_SET", "secret")
	t.Setenv("MINI_TEST_STATIC_AUTH_EMPTY", "")
	oauth := &config.AuthConfig{Type: config.AuthTypeOAuth2}
	custom := &config.AuthConfig{Type: config.AuthTypeOAuth2, Header: "X-Api-Key"}
	cases := []struct {
		name    string
		auth    *config.AuthConfig
		headers map[string]string
		want    bool
	}{
		{"exact key", oauth, map[string]string{"Authorization": "Bearer x"}, true},
		{"lowercase key", oauth, map[string]string{"authorization": "Bearer x"}, true},
		{"empty value", oauth, map[string]string{"Authorization": ""}, false},
		{"whitespace value", oauth, map[string]string{"Authorization": "  "}, false},
		{"env var reference stays literal", oauth, map[string]string{"Authorization": "${MINI_TEST_STATIC_AUTH_EMPTY}"}, true},
		{"literal env reference is a static header", oauth, map[string]string{"Authorization": "${MINI_TEST_STATIC_AUTH_SET}"}, true},
		{"auth token set", &config.AuthConfig{Type: config.AuthTypeOAuth2, Token: "tok"}, nil, true},
		{"auth token reference stays literal", &config.AuthConfig{Type: config.AuthTypeOAuth2, Token: "${MINI_TEST_STATIC_AUTH_EMPTY}"}, nil, true},
		{"custom auth header", custom, map[string]string{"X-Api-Key": "k"}, true},
		{"custom auth header configured but Authorization set", custom, map[string]string{"Authorization": "x"}, false},
		{"unrelated header only", oauth, map[string]string{"X-Tenant": "acme"}, false},
		{"nil auth", nil, map[string]string{"Authorization": "Bearer x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := config.ServerConfig{Auth: tc.auth, Headers: tc.headers}
			if got := sc.HasStaticAuthHeader(); got != tc.want {
				t.Errorf("HasStaticAuthHeader() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServerConfig_UsesOAuthLogin(t *testing.T) {
	oauth := &config.AuthConfig{Type: config.AuthTypeOAuth2}
	cases := []struct {
		name string
		sc   config.ServerConfig
		want bool
	}{
		{"http oauth2", config.ServerConfig{Transport: "http", Auth: oauth}, true},
		{"sse oauth2", config.ServerConfig{Transport: "sse", Auth: oauth}, true},
		{"streamable oauth2", config.ServerConfig{Transport: "streamable", Auth: oauth}, true},
		{"stdio oauth2", config.ServerConfig{Transport: "stdio", Auth: oauth}, false},
		{"nil auth", config.ServerConfig{Transport: "http"}, false},
		{"api_key type", config.ServerConfig{Transport: "http", Auth: &config.AuthConfig{Type: config.AuthTypeAPIKey}}, false},
		{"oauth2 with Authorization header", config.ServerConfig{Transport: "http", Auth: oauth, Headers: map[string]string{"Authorization": "Bearer x"}}, false},
		{"oauth2 with unrelated header only", config.ServerConfig{Transport: "http", Auth: oauth, Headers: map[string]string{"X-Tenant": "acme"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.UsesOAuthLogin(); got != tc.want {
				t.Errorf("UsesOAuthLogin() = %v, want %v", got, tc.want)
			}
		})
	}
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

func TestLoadResponseFormat(t *testing.T) {
	t.Run("toon accepted", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "response_format: toon\n")
		cfg, _ := mustLoadConfig(t, dir)
		if cfg.ResponseFormat != "toon" {
			t.Errorf("expected toon, got %q", cfg.ResponseFormat)
		}
	})
	t.Run("mini rejected naming toon as the replacement", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "response_format: mini\n")
		_, _, err := config.Load(dir)
		if err == nil || !strings.Contains(err.Error(), "toon") {
			t.Fatalf("expected error naming toon as the replacement, got %v", err)
		}
	})
	t.Run("unknown format rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "response_format: xml\n")
		expectLoadError(t, dir)
	})
}

func TestLoadProjectionFormat_rejectsMini(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), "name: gh\ncommand: gh-mcp\n")
	writeFile(t, filepath.Join(dir, "servers", "gh.proj.yaml"), "list_issues:\n  format: mini\n")
	_, _, err := config.Load(dir)
	if err == nil || !strings.Contains(err.Error(), "toon") {
		t.Fatalf("expected projection format error naming toon, got %v", err)
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

func TestLoadProjection_malformedYAML_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "srv.proj.yaml"), `not: valid: yaml: [`)
	expectLoadError(t, dir)
}

func TestLoadActions_malformedYAML_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "internal", "actions", "bad.yaml"), `not: valid: yaml: [`)
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidActionName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "internal", "actions", "bad.yaml"), "name: \"bad name\"\nserver: gh\ntool: list\n")
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "internal", "actions", "act.yaml"), "name: act\nserver: \"bad server\"\ntool: list\n")
	expectLoadActionsError(t, dir)
}

func TestLoadActions_invalidToolName_returnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "internal", "actions", "act.yaml"), "name: act\nserver: gh\ntool: \"bad/tool\"\n")
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
		{"DefaultDepthLimit", cfg.DefaultDepthLimit, 0},
		{"DefaultStringLimit", cfg.DefaultStringLimit, 2000},
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

func TestMergedHeaders_PlainHeader(t *testing.T) {
	sc := config.ServerConfig{Headers: map[string]string{"X-Foo": "bar"}}
	h := sc.MergedHeaders()
	if h["X-Foo"] != "bar" {
		t.Errorf("got %q", h["X-Foo"])
	}
}

func TestMergedHeaders_EnvReferenceStaysLiteral(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret")
	sc := config.ServerConfig{Headers: map[string]string{"Authorization": "Bearer ${MY_TOKEN}"}}
	h := sc.MergedHeaders()
	if h["Authorization"] != "Bearer ${MY_TOKEN}" {
		t.Errorf("got %q", h["Authorization"])
	}
}

func TestMergedHeaders_TrimsWhitespace(t *testing.T) {
	sc := config.ServerConfig{Headers: map[string]string{"X-Key": "  ${API_KEY}  "}}
	h := sc.MergedHeaders()
	if h["X-Key"] != "${API_KEY}" {
		t.Errorf("got %q", h["X-Key"])
	}
}

func TestMergedHeaders_BearerAuthKeepsEnvReferenceLiteral(t *testing.T) {
	t.Setenv("MY_TOKEN", "abc123")
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "bearer", Token: "${MY_TOKEN}"},
	}
	h := sc.MergedHeaders()
	if h["Authorization"] != "Bearer ${MY_TOKEN}" {
		t.Errorf("got %q", h["Authorization"])
	}
}

func TestMergedHeaders_APIKeyAuth(t *testing.T) {
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "apikey", Token: "rawkey", Header: "X-Api-Key"},
	}
	h := sc.MergedHeaders()
	if h["X-Api-Key"] != "rawkey" {
		t.Errorf("got %q", h["X-Api-Key"])
	}
}

func TestMergedHeaders_EmptyToken(t *testing.T) {
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "bearer", Token: ""},
	}
	h := sc.MergedHeaders()
	if _, ok := h["Authorization"]; ok {
		t.Error("expected no Authorization header when token is empty")
	}
}

func TestConfig_BrowserCommandFor(t *testing.T) {
	withBrowser := func(cmd string) config.ServerConfig {
		return config.ServerConfig{Auth: &config.AuthConfig{BrowserCmd: cmd}}
	}
	tests := []struct {
		name        string
		cfg         config.Config
		sc          config.ServerConfig
		wantCommand string
		wantOpen    bool
	}{
		{"per-server wins over global", config.Config{BrowserCommand: "global-cmd"}, withBrowser("per-server-cmd"), "per-server-cmd", true},
		{"global used when no per-server", config.Config{BrowserCommand: "global-cmd"}, withBrowser(""), "global-cmd", true},
		{"global used when server has no auth", config.Config{BrowserCommand: "global-cmd"}, config.ServerConfig{}, "global-cmd", true},
		{"neither set returns empty and still opens", config.Config{}, withBrowser(""), "", true},
		{"per-server with args wins", config.Config{BrowserCommand: "global-cmd"}, withBrowser("open -a Firefox"), "open -a Firefox", true},
		{"disabled overrides every command", config.Config{BrowserCommand: "global-cmd", DisableAuthBrowserOpen: true}, withBrowser("per-server-cmd"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			command, open := tc.cfg.BrowserCommandFor(tc.sc)
			if command != tc.wantCommand || open != tc.wantOpen {
				t.Errorf("BrowserCommandFor() = (%q, %v), want (%q, %v)", command, open, tc.wantCommand, tc.wantOpen)
			}
		})
	}
}

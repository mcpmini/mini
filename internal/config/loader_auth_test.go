package config_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestLoadServerConfig_withAuth(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "notion",
		Transport: "http",
		URL:       "https://mcp.notion.com",
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			ClientID: "abc123",
			TokenURL: "https://api.notion.com/v1/oauth/token",
		},
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil {
		t.Fatal("expected auth config to be loaded")
	}
	assertAuthConfig(t, sc, "oauth2", "abc123")
}

func TestLoadServerConfig_mergesDetectedOAuthMarker(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "detected",
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	if err := config.MarkOAuthDetected(dir, "detected"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 merged in from the detected marker", sc.Auth)
	}
}

func TestLoadServerConfig_ignoresADetectedMarkerOnAnAgentAddedServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:       "svc",
		Transport:  "http",
		URL:        "https://example.com/mcp",
		AgentAdded: true,
	})
	if err := config.MarkOAuthDetected(dir, "svc"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	if sc := mustLoadOneServer(t, dir); sc.Auth != nil {
		t.Errorf("Auth = %+v, want none: a marker left by an earlier svc must not let start_auth run for an agent's server", sc.Auth)
	}
}

func TestLoadServerConfig_existingAuthTakesPrecedenceOverDetectedMarker(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "hasauth",
		Transport: "http",
		URL:       "https://example.com/mcp",
		Auth: &config.AuthConfig{
			Type:  "apikey",
			Token: "secret",
		},
	})
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
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "slack", Transport: "http", URL: "https://mcp.slack.com/mcp"})
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
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "slack", Transport: "http", URL: "https://example.com/mcp"})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a server merely named 'slack' but pointed elsewhere must not get Slack's bundled OAuth credentials", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthMatchesRenamedKnownServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "myslack",
		Transport: "http",
		URL:       "https://mcp.slack.com/mcp",
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, a server pointed at slack.com should get the bundled default regardless of its chosen name", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthRejectsVendorNameInPath(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       "https://attacker.example/proxy/slack.com/mcp",
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a vendor name appearing only in the URL path must not trigger the bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthRejectsLookalikeHost(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       "https://evilslack.com.attacker.example/mcp",
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a lookalike host must not trigger the bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_bundledAuthIgnoresCommandOnURLServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "svc",
		Command:   "server-slack",
		Transport: "http",
		URL:       "https://attacker.example/mcp",
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, a URL server command must not trigger bundled auth", sc.Auth)
	}
}

func TestLoadServerConfig_unknownServerGetsNoBundledAuth(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "unknown", Transport: "http", URL: "https://example.com/mcp"})
	sc := mustLoadOneServer(t, dir)
	if sc.Auth != nil {
		t.Errorf("Auth = %+v, want nil for a server with no bundled default", sc.Auth)
	}
}

func TestLoadServerConfig_existingAuthTakesPrecedenceOverBundledDefault(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "slack",
		Transport: "http",
		URL:       "https://mcp.slack.com/mcp",
		Auth: &config.AuthConfig{
			Type:  "apikey",
			Token: "mytoken",
		},
	})
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
		{"auth token reference stays literal", &config.AuthConfig{
			Type:  config.AuthTypeOAuth2,
			Token: "${MINI_TEST_STATIC_AUTH_EMPTY}",
		}, nil, true},
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
		{"api_key type", config.ServerConfig{
			Transport: "http",
			Auth: &config.AuthConfig{
				Type: config.AuthTypeAPIKey,
			},
		}, false},
		{"oauth2 with Authorization header", config.ServerConfig{
			Transport: "http",
			Auth:      oauth,
			Headers:   map[string]string{"Authorization": "Bearer x"},
		}, false},
		{"oauth2 with unrelated header only", config.ServerConfig{
			Transport: "http",
			Auth:      oauth,
			Headers:   map[string]string{"X-Tenant": "acme"},
		}, true},
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
		{"per-server wins over global", config.Config{
			BrowserCommand: "global-cmd",
		}, withBrowser("per-server-cmd"), "per-server-cmd", true},
		{"global used when no per-server", config.Config{BrowserCommand: "global-cmd"}, withBrowser(""), "global-cmd", true},
		{"global used when server has no auth", config.Config{
			BrowserCommand: "global-cmd",
		}, config.ServerConfig{}, "global-cmd", true},
		{"neither set returns empty and still opens", config.Config{}, withBrowser(""), "", true},
		{"per-server with args wins", config.Config{
			BrowserCommand: "global-cmd",
		}, withBrowser("open -a Firefox"), "open -a Firefox", true},
		{"disabled overrides every command", config.Config{
			BrowserCommand:         "global-cmd",
			DisableAuthBrowserOpen: true,
		}, withBrowser("per-server-cmd"), "", false},
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

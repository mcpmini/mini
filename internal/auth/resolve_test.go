//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func resolveParams(configDir, serverName string) auth.ResolveEndpointsParams {
	return auth.ResolveEndpointsParams{ConfigDir: configDir, ServerName: serverName, Clock: clock.System()}
}

func TestApplyBearerToken(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantHeader string
	}{
		{"default header", "", "Authorization"},
		{"custom header", "X-Api-Key", "X-Api-Key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := &config.ServerConfig{Auth: &config.AuthConfig{Header: tc.header}}
			auth.ApplyBearerToken(sc, "tok123")
			if got := sc.Headers[tc.wantHeader]; got != "Bearer tok123" {
				t.Errorf("Headers[%q] = %q, want %q", tc.wantHeader, got, "Bearer tok123")
			}
		})
	}
}

func TestResolveEndpoints_cimd(t *testing.T) {
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2"},
	}
	if err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != auth.ClientMetadataURL {
		t.Errorf("ClientID = %q, want ClientMetadataURL", sc.Auth.ClientID)
	}
	if sc.Auth.ResourceURL != sc.URL {
		t.Errorf("ResourceURL = %q, want %q", sc.Auth.ResourceURL, sc.URL)
	}
}

func TestResolveEndpoints_cachedRegistrationBeforeCIMD(t *testing.T) {
	// Servers like Linear advertise CIMD but reject arbitrary metadata URLs.
	// A cached DCR client_id must win over CIMD to avoid re-fetching and failing.
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()

	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "cached-id"}); err != nil {
		t.Fatal(err)
	}

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2"},
	}
	if err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(dir, "srv")); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != "cached-id" {
		t.Errorf("ClientID = %q, want cached-id (cached DCR must beat CIMD)", sc.Auth.ClientID)
	}
}

func TestResolveEndpoints_dcrBeforeCIMD(t *testing.T) {
	// Servers like Linear advertise CIMD but reject unknown metadata URLs; DCR must be tried first.
	var dcrCalled bool
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dcrCalled = true
		json.NewEncoder(w).Encode(map[string]string{"client_id": "dcr-client-id"}) //nolint:errcheck
	}))
	defer regSrv.Close()

	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"registration_endpoint":                 regSrv.URL + "/register",
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2"},
	}
	if err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if !dcrCalled {
		t.Error("DCR endpoint was not called: CIMD must have been used instead")
	}
	if sc.Auth.ClientID != "dcr-client-id" {
		t.Errorf("ClientID = %q, want dcr-client-id", sc.Auth.ClientID)
	}
}


func TestResolveEndpoints_rejectsLoopbackDiscoveredEndpoints(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want string
	}{
		{
			name: "authorization endpoint",
			meta: map[string]any{
				"authorization_endpoint":           "http://127.0.0.1/authorize",
				"token_endpoint":                   "https://as.example.com/token",
				"code_challenge_methods_supported": []string{"S256"},
			},
			want: "authorization_endpoint",
		},
		{
			name: "token endpoint",
			meta: map[string]any{
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   "http://127.0.0.1/token",
				"code_challenge_methods_supported": []string{"S256"},
			},
			want: "token_endpoint",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", tc.meta)
			defer asSrv.Close()

			sc := &config.ServerConfig{
				URL:  asSrv.URL + "/mcp",
				Auth: &config.AuthConfig{Type: "oauth2", ClientID: "pre-configured"},
			}
			err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))
			if err == nil {
				t.Fatal("expected loopback endpoint validation error")
			}
			if got := err.Error(); got == "" || !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want mention of %s", got, tc.want)
			}
		})
	}
}


func TestResolveEndpoints_scopesAutoPopulatedFromPRM(t *testing.T) {
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()

	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_servers": []string{asSrv.URL},
			"scopes_supported":      []string{"channels:read", "chat:write"},
		})
	}))
	defer prmSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+prmSrv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	sc := &config.ServerConfig{
		URL:  mcpSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2", ClientID: "pre-configured"},
	}
	if err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if len(sc.Auth.Scopes) != 2 || sc.Auth.Scopes[0] != "channels:read" {
		t.Errorf("Scopes = %v, want [channels:read chat:write]", sc.Auth.Scopes)
	}
}

func TestResolveEndpoints_userScopesNotOverwritten(t *testing.T) {
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
		"scopes_supported":                 []string{"channels:read"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2", ClientID: "pre-configured", Scopes: []string{"custom:scope"}},
	}
	if err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if len(sc.Auth.Scopes) != 1 || sc.Auth.Scopes[0] != "custom:scope" {
		t.Errorf("Scopes = %v, want user-configured [custom:scope] preserved", sc.Auth.Scopes)
	}
}

func TestValidateOAuthServer(t *testing.T) {
	tests := []struct {
		name    string
		sc      config.ServerConfig
		wantErr bool
	}{
		{"no auth", config.ServerConfig{Name: "s"}, true},
		{"apikey auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "apikey"}}, true},
		{"oauth2 auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "oauth2"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidateOAuthServer("s", tc.sc)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateOAuthServer() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

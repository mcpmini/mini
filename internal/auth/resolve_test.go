//go:build test

package auth_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

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
	if err := auth.ResolveEndpoints(context.Background(), t.TempDir(), "srv", sc); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != auth.ClientMetadataURL {
		t.Errorf("ClientID = %q, want ClientMetadataURL", sc.Auth.ClientID)
	}
	if sc.Auth.ResourceURL != sc.URL {
		t.Errorf("ResourceURL = %q, want %q", sc.Auth.ResourceURL, sc.URL)
	}
}

func TestResolveEndpoints_cachedRegistrationBeforesCIMD(t *testing.T) {
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
	if err := auth.ResolveEndpoints(context.Background(), dir, "srv", sc); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != "cached-id" {
		t.Errorf("ClientID = %q, want cached-id (cached DCR must beat CIMD)", sc.Auth.ClientID)
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

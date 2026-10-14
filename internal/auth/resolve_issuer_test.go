//go:build test

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestSelectClient_bindsStoredRegistrationToIssuer(t *testing.T) {
	meta := &auth.ServerMeta{
		Issuer: "https://as.example", TokenURL: "https://as.example/token", CIMDSupported: true,
	}
	for _, tc := range []struct {
		name string
		reg  *auth.Registration
	}{
		{name: "matching issuer", reg: &auth.Registration{Issuer: meta.Issuer, ClientID: "registered"}},
		{name: "wrong issuer", reg: &auth.Registration{Issuer: "https://other.example", ClientID: "attacker", ClientSecret: "secret", TokenEndpointAuthMethod: "client_secret_basic"}},
		{name: "missing issuer", reg: &auth.Registration{ClientID: "legacy"}},
		{name: "expired secret", reg: &auth.Registration{Issuer: meta.Issuer, ClientID: "expired", ClientSecret: "secret", TokenEndpointAuthMethod: "client_secret_basic", ClientSecretExpiresAt: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			choice, err := auth.SelectClient(auth.ClientSelectionParams{
				AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2}, Registration: tc.reg,
				Meta: meta, Now: time.Unix(2, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "matching issuer" {
				if choice.Client.Source != auth.ClientRegistered || choice.Client.ID != tc.reg.ClientID ||
					choice.Client.AuthMethod != "" {
					t.Fatalf("selected client = %+v, want matching public registration", choice.Client)
				}
				return
			}
			if choice.Client.Source == auth.ClientRegistered || choice.Client.ID != auth.ClientMetadataURL ||
				choice.Client.Secret != "" {
				t.Fatalf("selected client = %+v, want public CIMD fallback", choice.Client)
			}
		})
	}
}

func TestSelectClient_defaultsConfidentialAuthMethodWithoutMutatingRegistration(t *testing.T) {
	reg := &auth.Registration{
		Issuer: "https://as.example", ClientID: "confidential", ClientSecret: "secret",
	}
	choice, err := auth.SelectClient(auth.ClientSelectionParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2}, Registration: reg,
		Meta: &auth.ServerMeta{Issuer: reg.Issuer, TokenURL: "https://as.example/token"},
		Now:  time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Client.Source != auth.ClientRegistered || choice.Client.AuthMethod != "client_secret_basic" {
		t.Fatalf("selected client = %+v, want confidential basic client", choice.Client)
	}
	if reg.TokenEndpointAuthMethod != "" {
		t.Fatalf("selection mutated registration method to %q", reg.TokenEndpointAuthMethod)
	}
}

func TestSelectClient_configuredClientSkipsInvalidRegistrationFile(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir+"/internal/srv.dcr.json", "{")
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example/authorize", "token_endpoint": "https://as.example/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer as.Close()
	sc := &config.ServerConfig{
		URL:  as.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "my-own-client"},
	}
	client, _, err := auth.ResolveEndpointsWithClient(context.Background(), sc, resolveParams(dir, "srv"))
	if err != nil {
		t.Fatal(err)
	}
	if client != (auth.Client{Source: auth.ClientConfigured, ID: "my-own-client"}) {
		t.Fatalf("selected client = %+v, want configured client", client)
	}
}

func TestResolveEndpoints_refusesTokenOverrideBeforeDynamicRegistration(t *testing.T) {
	var registrations int
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registrations++
		w.WriteHeader(http.StatusOK)
	}))
	defer reg.Close()
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example/authorize", "token_endpoint": "https://as.example/token",
		"registration_endpoint": reg.URL, "code_challenge_methods_supported": []string{"S256"},
	})
	defer as.Close()
	sc := &config.ServerConfig{
		URL:  as.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "https://other.example/token"},
	}
	err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))
	if err == nil || !strings.Contains(err.Error(), "token_url") {
		t.Fatalf("ResolveEndpoints error = %v, want token_url conflict", err)
	}
	if registrations != 0 {
		t.Fatalf("registration requests = %d, want no DCR side effect", registrations)
	}
}

func TestResolveEndpoints_all404FallbackAllowsConfiguredClientOnly(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	sc := &config.ServerConfig{
		URL:  srv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "configured"},
	}
	client, meta, err := auth.ResolveEndpointsWithClient(context.Background(), sc, resolveParams(t.TempDir(), "srv"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Issuer != "" || client.Source != auth.ClientConfigured {
		t.Fatalf("metadata/client = %+v/%+v, want no discovered issuer and configured client", meta, client)
	}
	missing := &config.ServerConfig{URL: srv.URL + "/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	if _, _, err := auth.ResolveEndpointsWithClient(
		context.Background(), missing, resolveParams(t.TempDir(), "srv"),
	); err == nil {
		t.Fatal("unconfigured client logged in with all-404 metadata fallback")
	} else if !strings.Contains(err.Error(), "configure your own client") {
		t.Fatalf("unexpected fallback error: %v", err)
	}
}

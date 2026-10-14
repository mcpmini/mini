//go:build test

package provider_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestRefreshAuthorization_canonicalServerURLOwnsResource(t *testing.T) {
	endpoint := authtest.NewTokenServer(t)
	cases := []struct {
		name         string
		serverURL    string
		resourceURL  string
		wantResource string
	}{
		{
			name:         "ServerURL canonicalized when ResourceURL empty",
			serverURL:    "HTTPS://Example.COM:443/mcp",
			resourceURL:  "",
			wantResource: "https://example.com/mcp",
		},
		{
			name:         "independent ResourceURL cannot change saved resource",
			serverURL:    "HTTPS://Example.COM:443/mcp",
			resourceURL:  "https://other.example/api",
			wantResource: "https://example.com/mcp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			authtest.SaveToken(
				t,
				authtest.TokenFile{
					Resource:   "https://example.com/mcp",
					TokenURL:   endpoint.Srv.URL + "/token",
					Client:     auth.Client{Source: auth.ClientConfigured, ID: "cid"},
					ConfigDir:  dir,
					ServerName: "srv",
					Token:      storedToken(time.Time{}),
				},
			)
			p, err := provider.New(provider.Params{
				AuthConfig: &config.AuthConfig{
					Type: config.AuthTypeOAuth2, ClientID: "cid",
					TokenURL: endpoint.Srv.URL + "/token", ResourceURL: tc.resourceURL,
				},
				ConfigDir: dir, ServerName: "srv", ServerURL: tc.serverURL, Clock: clock.NewFake(),
			})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
				t.Fatalf("RefreshAuthorization: %v", err)
			}
			endpoint.Mu.Lock()
			got := endpoint.LastResource
			endpoint.Mu.Unlock()
			if got != tc.wantResource {
				t.Errorf("resource = %q, want %q", got, tc.wantResource)
			}
		})
	}
}

func TestRefreshAuthorization_externalLoginAdoptsItsRecordedRegisteredClient(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	clk := clock.NewFake()

	authtest.SaveRegistration(t, authtest.RegistrationFile{
		ConfigDir:  dir,
		ServerName: "srv",
		Registration: &auth.Registration{
			ClientID: "dcr-v1",
		},
	})
	initialTok := &oauth2.Token{
		AccessToken: "initial-access", RefreshToken: "initial-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientRegistered, ID: "dcr-v1"},
			ConfigDir:  dir,
			ServerName: "srv",
			Token:      initialTok,
		},
	)
	p, err := provider.New(provider.Params{
		ServerURL:  "https://resource.example.com/mcp",
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, authorizationErr := p.Authorization(context.Background()); authorizationErr != nil {
		t.Fatal(authorizationErr)
	}

	authtest.SaveRegistration(t, authtest.RegistrationFile{
		ConfigDir:  dir,
		ServerName: "srv",
		Registration: &auth.Registration{
			ClientID: "dcr-v2",
		},
	})
	freshTok := &oauth2.Token{
		AccessToken: "auth-access", RefreshToken: "auth-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientRegistered, ID: "dcr-v2"},
			ConfigDir:  dir,
			ServerName: "srv",
			Token:      freshTok,
		},
	)

	got, err := p.RefreshAuthorization(context.Background(), "Bearer initial-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer auth-access" {
		t.Fatalf("expected adopted token, got %q", got)
	}
	if endpoint.Hits.Load() != 0 {
		t.Fatalf("token endpoint hit during adoption, want 0 hits")
	}

	clk.Advance(2 * time.Hour)
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization after expiry: %v", err)
	}

	endpoint.Mu.Lock()
	clientIDForm, clientIDBasic := endpoint.LastClientID, endpoint.LastBasicAuth
	endpoint.Mu.Unlock()

	if clientIDForm != "dcr-v2" && clientIDBasic != "dcr-v2" {
		t.Errorf(
			"token endpoint client_id = form:%q basic:%q, want issuing client dcr-v2 from the external login",
			clientIDForm,
			clientIDBasic,
		)
	}
}

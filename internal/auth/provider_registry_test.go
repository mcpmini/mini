//go:build test

package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestProviderRegistry_sameServer_returnsSameProvider(t *testing.T) {
	registry := auth.NewProviderRegistry()
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		Clock:      clock.NewFake(),
	}
	first, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("registry returned different providers for the same server")
	}
}

func TestProviderRegistry_changedServerURL_rejectedAndOriginalKept(t *testing.T) {
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	registry := auth.NewProviderRegistry()
	original, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	changed := params
	changed.ServerURL = "https://other.example.com/mcp"
	if _, err := registry.GetOrCreate(changed); err == nil {
		t.Fatal("incompatible provider identity was accepted")
	}
	again, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if original != again {
		t.Fatal("rejected configuration mutated the registered provider")
	}
}

func TestProviderRegistry_authConfigDrift_redialReusesProvider(t *testing.T) {
	cases := []struct {
		name    string
		initial func(t *testing.T, dir string) auth.ProviderParams
		between func(t *testing.T, dir string, reg *auth.ProviderRegistry)
		redial  func(t *testing.T, dir string) auth.ProviderParams
	}{
		{
			name: "expired_client_secret",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				if err := auth.SaveRegistration(dir, "srv", &auth.Registration{
					ClientID: "dcr-client", ClientSecret: "secret",
					TokenEndpointAuthMethod: "client_secret_basic",
					ClientSecretExpiresAt:   100,
				}); err != nil {
					t.Fatal(err)
				}
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFakeAt(time.Unix(0, 0)),
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFakeAt(time.Unix(200, 0)),
				}
			},
		},
		{
			name: "external_auth_updates_registration",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
			between: func(t *testing.T, dir string, reg *auth.ProviderRegistry) {
				if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "dcr-new"}); err != nil {
					t.Fatal(err)
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
		},
		{
			name: "redial with undiscovered config after commit",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
			between: func(t *testing.T, dir string, reg *auth.ProviderRegistry) {
				committed := auth.ProviderParams{
					AuthConfig: &config.AuthConfig{
						Type: config.AuthTypeOAuth2, ClientID: "discovered",
						AuthURL: "https://as.example.com/auth", TokenURL: "https://as.example.com/token",
					},
					ConfigDir: dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
				tok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
				if err := reg.CommitAuthorizedToken(committed, tok); err != nil {
					t.Fatalf("CommitAuthorizedToken: %v", err)
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			reg := auth.NewProviderRegistry()
			first, err := reg.GetOrCreate(tc.initial(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if tc.between != nil {
				tc.between(t, dir, reg)
			}
			second, err := reg.GetOrCreate(tc.redial(t, dir))
			if err != nil {
				t.Fatalf("re-dial rejected: %v", err)
			}
			if first != second {
				t.Fatal("re-dial returned a different provider")
			}
		})
	}
}

func TestProviderRegistry_close_abortsInFlightRefresh(t *testing.T) {
	dir := t.TempDir()
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: epoch.Add(-time.Second)}
	if err := auth.Save(dir, "srv", expired); err != nil {
		t.Fatal(err)
	}

	endpoint := newMockAuthServer(t)
	received, release := gateNextTokenRequest(endpoint)
	t.Cleanup(func() { release() })

	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "c", TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clock.NewFakeAt(epoch),
	}
	registry := auth.NewProviderRegistry()
	provider, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		provider.Authorization(context.Background()) //nolint:errcheck
	}()
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}

	registry.Close()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("registry.Close() did not abort in-flight refresh within 5s")
	}
}

//go:build test

package provider_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestNewProvider_savedRegisteredClient_usesItsConfidentialCredentials(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	client := auth.Client{
		Source:     auth.ClientRegistered,
		ID:         "issued-client",
		Secret:     "issued-secret",
		AuthMethod: "client_secret_basic",
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     client,
			Token:      storedToken(time.Time{}),
		},
	)
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Registration: &auth.Registration{
				ClientID:                "issued-client",
				ClientSecret:            "replacement-secret",
				TokenEndpointAuthMethod: "client_secret_basic",
			},
		},
	)
	p, err := provider.New(
		provider.Params{
			AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
			ConfigDir:  dir,
			ServerName: "srv",
			ServerURL:  "https://resource.example.com/mcp",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	endpoint.Mu.Lock()
	user, secret := endpoint.LastBasicAuth, endpoint.LastBasicSecret
	endpoint.Mu.Unlock()
	if user != "issued-client" || secret != "issued-secret" {
		t.Fatalf("refresh client=%q Basic secret=%q; want saved registered client", user, secret)
	}
}

func TestNewProvider_inconsistentRegistration_doesNotChangeSavedLogin(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientMetadataDocument, ID: auth.ClientMetadataURL},
			Token:      storedToken(time.Time{}),
		},
	)
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Registration: &auth.Registration{
				ClientID:                "orphan",
				ClientSecret:            "orphan-secret",
				TokenEndpointAuthMethod: "none",
			},
		},
	)
	p, err := provider.New(
		provider.Params{
			AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
			ConfigDir:  dir,
			ServerName: "srv",
			ServerURL:  "https://resource.example.com/mcp",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
		t.Fatalf("client=%q, want saved metadata client", got)
	}
}

func TestNewProvider_noRegistration_usesSavedMetadataClient(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientMetadataDocument, ID: auth.ClientMetadataURL},
			Token:      storedToken(time.Time{}),
		},
	)
	p, err := provider.New(
		provider.Params{
			AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.Srv.URL + "/token"},
			ConfigDir:  dir,
			ServerName: "srv",
			ServerURL:  "https://resource.example.com/mcp",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
		t.Fatalf("client=%q, want saved metadata client", got)
	}
}

func TestNewProvider_concurrentConstruction_leavesSharedConfigUnchanged(t *testing.T) {
	dir := t.TempDir()
	shared := &config.AuthConfig{
		Type:            config.AuthTypeOAuth2,
		ClientID:        "cid",
		TokenURL:        "http://localhost:1/token",
		Scopes:          []string{"read"},
		ExtraAuthParams: map[string]string{"prompt": "consent"},
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   shared.TokenURL,
			Client:     auth.Client{Source: auth.ClientConfigured, ID: "cid"},
			Token:      storedToken(time.Time{}),
		},
	)
	want := *shared
	want.Scopes = slices.Clone(shared.Scopes)
	want.ExtraAuthParams = maps.Clone(shared.ExtraAuthParams)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			p, err := provider.New(
				provider.Params{
					AuthConfig: shared,
					ConfigDir:  dir,
					ServerName: "srv",
					ServerURL:  "https://resource.example.com/mcp",
					Clock:      clock.NewFake(),
				},
			)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = p.Authorization(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(*shared, want) {
		t.Fatalf("shared configuration changed: got %+v want %+v", *shared, want)
	}
}

func TestNewProvider_explicitClientID_ignoresStoredRegistration(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Registration: &auth.Registration{
				ClientID:                "stale-id",
				ClientSecret:            "stale-secret",
				TokenEndpointAuthMethod: "client_secret_basic",
			},
		},
	)
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientConfigured, ID: "manual-id"},
			Token:      storedToken(time.Time{}),
		},
	)
	p, err := provider.New(
		provider.Params{
			AuthConfig: &config.AuthConfig{
				Type:     config.AuthTypeOAuth2,
				ClientID: "manual-id",
				TokenURL: endpoint.Srv.URL + "/token",
			},
			ConfigDir:  dir,
			ServerName: "srv",
			ServerURL:  "https://resource.example.com/mcp",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got != "manual-id" {
		t.Fatalf("client=%q, want manual-id", got)
	}
}

func TestNewProvider_nilAuthConfig_returnsError(t *testing.T) {
	if _, err := provider.New(provider.Params{ConfigDir: t.TempDir(), ServerName: "srv"}); err == nil {
		t.Fatal("nil AuthConfig accepted")
	}
}

func TestNewProvider_nilClock_worksWithStoredToken(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Now().Add(time.Hour))})
	p, err := provider.New(
		provider.Params{
			AuthConfig: &config.AuthConfig{
				Type:     config.AuthTypeOAuth2,
				ClientID: "cid",
				TokenURL: f.endpoint.Srv.URL + "/token",
			},
			ConfigDir:  f.dir,
			ServerName: "srv",
			ServerURL:  "https://resource.example.com/mcp",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Authorization(context.Background())
	if err != nil || got != "Bearer stored-access" {
		t.Fatalf("Authorization=%q,%v", got, err)
	}
}

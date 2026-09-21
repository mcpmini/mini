//go:build test

package auth_test

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProviderCache_commitHydratesRegistration(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()

	reg1 := &auth.Registration{ClientID: "dcr-v1", ClientSecret: "secret-v1", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg1); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()
	p, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	endpoint.mu.Lock()
	gotV1 := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	if gotV1 != "dcr-v1" {
		t.Errorf("initial basic auth user = %q, want dcr-v1", gotV1)
	}

	reg2 := &auth.Registration{ClientID: "dcr-v2", ClientSecret: "secret-v2", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg2); err != nil {
		t.Fatal(err)
	}
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	endpoint.mu.Lock()
	endpoint.accessToken, endpoint.refreshToken = "new-access2", "new-refresh2"
	endpoint.mu.Unlock()
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("post-commit refresh: %v", err)
	}
	endpoint.mu.Lock()
	gotV2 := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	if gotV2 != "dcr-v2" {
		t.Errorf("post-commit basic auth user = %q, want dcr-v2 (commit must hydrate new registration)", gotV2)
	}
}

func TestProviderCache_commitSaveFailureLeavesProviderUnchanged(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()
	p, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err == nil {
		t.Fatal("expected CommitAuthorizedToken to fail when persist is denied")
	}
	os.Chmod(internal, 0700) //nolint:errcheck

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after failed commit: %v", err)
	}
	if got == "Bearer browser-access" {
		t.Error("failed commit must not update in-memory token")
	}
}

func TestOAuthReconnect_perSessionProviderSeesAuthorizedToken(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()
	primary, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	perSession, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if primary != perSession {
		t.Fatal("GetOrCreate must return the same provider for equivalent params")
	}
	browserTok := &oauth2.Token{AccessToken: "authorized-access", RefreshToken: "authorized-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	for name, p := range map[string]transport.AuthorizationProvider{"primary": primary, "perSession": perSession} {
		got, err := p.Authorization(context.Background())
		if err != nil {
			t.Fatalf("%s Authorization: %v", name, err)
		}
		if got != "Bearer authorized-access" {
			t.Errorf("%s got %q, want Bearer authorized-access", name, got)
		}
	}
}

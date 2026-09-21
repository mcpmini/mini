//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestProviderCache_reusesProviderPerServer(t *testing.T) {
	cache := auth.NewProviderCache()
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		Clock:      clock.NewFake(),
	}
	first, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("cache returned different providers for the same server")
	}
}

func TestProviderCache_commitUpdatesEffectiveIdentity(t *testing.T) {
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	cache := auth.NewProviderCache()
	before, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	authorized := params
	authorized.AuthConfig = &config.AuthConfig{
		Type:     config.AuthTypeOAuth2,
		ClientID: "discovered-client",
		AuthURL:  "https://auth.example.com/authorize",
		TokenURL: "https://auth.example.com/token",
	}
	token := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := cache.CommitAuthorizedToken(authorized, token); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	after, err := cache.GetOrCreate(authorized)
	if err != nil {
		t.Fatalf("GetOrCreate after commit: %v", err)
	}
	if before != after {
		t.Fatal("authorized token commit replaced the provider")
	}
	got, err := after.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
}

func TestProviderCache_incompatibleParamsRejectedWithoutMutation(t *testing.T) {
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	cache := auth.NewProviderCache()
	original, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	changed := params
	changed.ServerURL = "https://other.example.com/mcp"
	if _, err := cache.GetOrCreate(changed); err == nil {
		t.Fatal("incompatible provider identity was accepted")
	}
	again, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if original != again {
		t.Fatal("rejected configuration mutated the cached provider")
	}
}

func TestProviderCache_commitWaitsForRefreshAndNewTokenWins(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	endpointHit := make(chan struct{})
	releaseEndpoint := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(endpointHit)
		<-releaseEndpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "refresh-result", "refresh_token": "refresh-rotated",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenServer.Close)
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: tokenServer.URL,
			TokenEndpointAuthMethod: "client_secret_post",
		},
		ConfigDir: dir, ServerName: "srv", ServerURL: "https://mcp.example.com/mcp", Clock: clk,
	}
	cache := auth.NewProviderCache()
	provider, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		refreshDone <- err
	}()
	<-endpointHit
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- cache.CommitAuthorizedToken(params, &oauth2.Token{
			AccessToken: "browser-access", RefreshToken: "browser-refresh",
		})
	}()
	runtime.Gosched()
	select {
	case err := <-commitDone:
		t.Fatalf("commit returned during refresh: %v", err)
	default:
	}
	close(releaseEndpoint)
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := <-commitDone; err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := provider.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "browser-access" {
		t.Fatalf("saved access token = %q, want browser token", saved.AccessToken)
	}
}

func TestProvider_reloadExternalAuthorizationBefore401Refresh(t *testing.T) {
	dir := t.TempDir()
	oldToken := storedToken(time.Time{})
	if err := auth.Save(dir, "srv", oldToken); err != nil {
		t.Fatal(err)
	}
	endpoint := newTokenEndpoint(t)
	provider, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com/mcp", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}
	external := &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}
	if err := auth.Save(dir, "srv", external); err != nil {
		t.Fatal(err)
	}
	got, err := provider.RefreshAuthorization(context.Background(), "Bearer "+oldToken.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer external-access" {
		t.Fatalf("Authorization = %q, want external token", got)
	}
	if endpoint.hits.Load() != 0 {
		t.Fatalf("token endpoint hits = %d, want disk handoff without refresh", endpoint.hits.Load())
	}
}

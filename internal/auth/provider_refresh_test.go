//go:build test

package auth_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestProviderRefresh_persistsRotatedRefreshToken(t *testing.T) {
	f := newProviderFixture(t, providerSetup{
		Token: storedToken(time.Time{}),
		Auth:  &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", ResourceURL: "https://resource.example/mcp"},
	})
	got, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("header = %q, want refreshed token", got)
	}
	if f.endpoint.lastGrant != "refresh_token" || f.endpoint.lastRefresh != "stored-refresh" {
		t.Errorf("refresh used grant=%q token=%q", f.endpoint.lastGrant, f.endpoint.lastRefresh)
	}
	if f.endpoint.lastResource != "https://resource.example/mcp" {
		t.Errorf("refresh resource = %q", f.endpoint.lastResource)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load persisted token: %v", err)
	}
	if saved.AccessToken != "new-access" || saved.RefreshToken != "rotated-refresh" {
		t.Errorf("persisted access=%q refresh=%q, want rotated pair", saved.AccessToken, saved.RefreshToken)
	}
}

func TestProviderRefresh_httpFailureNamesRemedy(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.status.Store(http.StatusInternalServerError)
	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected refresh failure")
	}
	if !strings.Contains(err.Error(), "mini auth srv") || !strings.Contains(err.Error(), "srv requires re-authorization") {
		t.Errorf("error should name server and remedy, got: %v", err)
	}
}

func TestProviderRefresh_persistFailureKeepsRotatedTokenInMemory(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	internal := f.dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("refresh must succeed despite persist failure: %v", err)
	}
	got, err := f.provider.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after persist failure: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("next call must use rotated in-memory token, got %q", got)
	}
	if hits := f.endpoint.hits.Load(); hits != 1 {
		t.Errorf("rotated token must be reused without another refresh, got %d hits", hits)
	}

	f.endpoint.mu.Lock()
	f.endpoint.accessToken, f.endpoint.refreshToken = "second-access", "second-refresh"
	f.endpoint.mu.Unlock()
	os.Chmod(internal, 0700) //nolint:errcheck
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer new-access"); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if f.endpoint.lastRefresh != "rotated-refresh" {
		t.Errorf("second refresh must use the rotated refresh token, sent %q", f.endpoint.lastRefresh)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load after persist retry: %v", err)
	}
	if saved.AccessToken != "second-access" || saved.RefreshToken != "second-refresh" {
		t.Errorf("persist must be retried on next refresh, got access=%q refresh=%q", saved.AccessToken, saved.RefreshToken)
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

func TestRefreshAuthorization_singleFlightOn401(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
			if err != nil {
				t.Error(err)
				return
			}
			results <- v
		}()
	}
	wg.Wait()
	close(results)
	if hits := f.endpoint.hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", hits)
	}
	for v := range results {
		if v != "Bearer new-access" {
			t.Errorf("got %q, want Bearer new-access", v)
		}
	}
}

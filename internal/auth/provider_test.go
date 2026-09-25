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

func TestAuthorization_nearExpiry_refreshesBeforeTokenExpires(t *testing.T) {
	epoch := clock.NewFake().Now()
	shortLived := storedToken(epoch.Add(30 * time.Second))
	shortLived.ExpiresIn = 30
	cases := []struct {
		name        string
		token       *oauth2.Token
		wantHeader  string
		wantRefresh int32
	}{
		{"before refresh window keeps stored token", storedToken(epoch.Add(10 * time.Minute)), "Bearer stored-access", 0},
		{"exactly at expiry minus window refreshes", storedToken(epoch.Add(5 * time.Minute)), "Bearer new-access", 1},
		{"inside refresh window refreshes", storedToken(epoch.Add(4 * time.Minute)), "Bearer new-access", 1},
		{"already expired refreshes", storedToken(epoch.Add(-time.Hour)), "Bearer new-access", 1},
		{"short-lived token uses bounded skew", shortLived, "Bearer stored-access", 0},
		{"zero expiry never refreshes proactively", storedToken(time.Time{}), "Bearer stored-access", 0},
		{"no refresh token skips proactive refresh", &oauth2.Token{AccessToken: "stored-access", Expiry: epoch.Add(time.Minute)}, "Bearer stored-access", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProviderFixture(t, providerSetup{Token: tc.token})
			got, err := f.provider.Authorization(context.Background())
			if err != nil {
				t.Fatalf("Authorization: %v", err)
			}
			if got != tc.wantHeader {
				t.Errorf("header = %q, want %q", got, tc.wantHeader)
			}
			if hits := f.endpoint.hits.Load(); hits != tc.wantRefresh {
				t.Errorf("token endpoint hits = %d, want %d", hits, tc.wantRefresh)
			}
		})
	}
}

func TestAuthorization_noStoredToken_returnsReauthRemedy(t *testing.T) {
	f := newProviderFixture(t, providerSetup{})
	_, err := f.provider.Authorization(context.Background())
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "mini auth srv") {
		t.Errorf("error should name remedy, got: %v", err)
	}
}

func TestAuthorization_concurrentCallers_refreshOnce(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(clock.NewFake().Now())})
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.provider.Authorization(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Authorization: %v", err)
		}
	}
	if hits := f.endpoint.hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", hits)
	}
}

func TestAuthorization_proactiveRefreshFails_servesStillValidToken(t *testing.T) {
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Run("still_valid_returns_current_token", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{
			Token: storedToken(epoch.Add(60 * time.Second)),
		})
		f.endpoint.status.Store(http.StatusServiceUnavailable)

		got, err := f.provider.Authorization(context.Background())
		if err != nil {
			t.Fatalf("proactive refresh error must not fail when token still valid: %v", err)
		}
		if got != "Bearer stored-access" {
			t.Errorf("Authorization = %q, want Bearer stored-access", got)
		}
	})
	t.Run("expired_propagates_error", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{
			Token: storedToken(epoch.Add(-time.Second)),
		})
		f.endpoint.status.Store(http.StatusServiceUnavailable)

		_, err := f.provider.Authorization(context.Background())
		if err == nil {
			t.Fatal("expired token must propagate refresh error")
		}
	})
}

func TestRefreshAuthorization_rotatedRefreshToken_isPersisted(t *testing.T) {
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
	f.endpoint.mu.Lock()
	grant, refresh, resource := f.endpoint.lastGrant, f.endpoint.lastRefresh, f.endpoint.lastResource
	f.endpoint.mu.Unlock()
	if grant != "refresh_token" || refresh != "stored-refresh" {
		t.Errorf("refresh used grant=%q token=%q", grant, refresh)
	}
	if resource != "https://resource.example/mcp" {
		t.Errorf("refresh resource = %q", resource)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load persisted token: %v", err)
	}
	if saved.AccessToken != "new-access" || saved.RefreshToken != "rotated-refresh" {
		t.Errorf("persisted access=%q refresh=%q, want rotated pair", saved.AccessToken, saved.RefreshToken)
	}
}

func TestRefreshAuthorization_tokenEndpointError_returnsReauthRemedy(t *testing.T) {
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

func TestRefreshAuthorization_saveFails_keepsRotatedTokenInMemory(t *testing.T) {
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

	f.endpoint.accessToken, f.endpoint.refreshToken = "second-access", "second-refresh"
	os.Chmod(internal, 0700) //nolint:errcheck
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer new-access"); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	f.endpoint.mu.Lock()
	lastRefresh := f.endpoint.lastRefresh
	f.endpoint.mu.Unlock()
	if lastRefresh != "rotated-refresh" {
		t.Errorf("second refresh must use the rotated refresh token, sent %q", lastRefresh)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load after persist retry: %v", err)
	}
	if saved.AccessToken != "second-access" || saved.RefreshToken != "second-refresh" {
		t.Errorf("persist must be retried on next refresh, got access=%q refresh=%q", saved.AccessToken, saved.RefreshToken)
	}
}

func TestRefreshAuthorization_newerStoredToken_usedWithoutRefreshing(t *testing.T) {
	dir := t.TempDir()
	oldToken := storedToken(time.Time{})
	if err := auth.Save(dir, "srv", oldToken); err != nil {
		t.Fatal(err)
	}
	endpoint := newMockAuthServer(t)
	provider, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL + "/token"},
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

func TestRefreshAuthorization_concurrent401s_refreshOnce(t *testing.T) {
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

func TestRefreshAuthorization_callerCancelled_stillPersistsRotatedToken(t *testing.T) {
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}
	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "rotated-access"
	endpoint.refreshToken = "rotated-refresh"
	received, release := holdMockServer(endpoint)

	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	refreshDone := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(ctx, "Bearer old-access")
		refreshDone <- err
	}()
	<-received
	cancel()
	release()

	if err := <-refreshDone; err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer rotated-access" {
		t.Errorf("Authorization = %q, want rotated token", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "rotated-refresh" {
		t.Errorf("persisted refresh = %q, want rotated-refresh", saved.RefreshToken)
	}
	if endpoint.hits.Load() != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (rotated token reused)", endpoint.hits.Load())
	}
}

func TestRefreshAuthorization_noTokenURL_returnsReauthRemedy(t *testing.T) {
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{AccessToken: "tok", RefreshToken: "ref"}); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.RefreshAuthorization(context.Background(), "Bearer tok")
	if err == nil {
		t.Fatal("expected error for missing token URL")
	}
	if !strings.Contains(err.Error(), "mini auth srv") {
		t.Errorf("error should name remedy, got: %v", err)
	}
}

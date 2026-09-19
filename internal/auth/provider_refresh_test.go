//go:build test

package auth_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
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

func TestProviderRefresh_transientBudgetExhaustedDoesNotRequireReauth(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.status.Store(http.StatusInternalServerError)
	errCh := make(chan error, 1)
	go func() {
		_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		errCh <- err
	}()
	advanceForBackoffs(t, f.clock, []time.Duration{time.Second, 2 * time.Second})
	err := <-errCh
	if err == nil {
		t.Fatal("expected refresh failure after budget exhaustion")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("HTTP 500 is transient; must not produce ErrReauthRequired: %v", err)
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

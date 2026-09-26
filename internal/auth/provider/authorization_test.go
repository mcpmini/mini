//go:build test

package provider_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
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
		{
			"short-lived 10pct boundary at window refreshes",
			&oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: epoch.Add(60 * time.Second), ExpiresIn: 600},
			"Bearer new-access", 1,
		},
		{
			"short-lived 10pct boundary just outside window keeps stored token",
			&oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: epoch.Add(61 * time.Second), ExpiresIn: 600},
			"Bearer stored-access", 0,
		},
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
			if hits := f.endpoint.Hits.Load(); hits != tc.wantRefresh {
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
	if hits := f.endpoint.Hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", hits)
	}
}

func TestAuthorization_proactiveRefreshFails_servesStillValidToken(t *testing.T) {
	epoch := clock.NewFake().Now()
	t.Run("still valid returns current token", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{
			Token: storedToken(epoch.Add(60 * time.Second)),
		})
		f.endpoint.Status.Store(http.StatusServiceUnavailable)

		got, err := f.provider.Authorization(context.Background())
		if err != nil {
			t.Fatalf("proactive refresh error must not fail when token still valid: %v", err)
		}
		if got != "Bearer stored-access" {
			t.Errorf("Authorization = %q, want Bearer stored-access", got)
		}
	})
	t.Run("expired propagates error", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{
			Token: storedToken(epoch.Add(-time.Second)),
		})
		f.endpoint.Status.Store(http.StatusServiceUnavailable)

		_, err := f.provider.Authorization(context.Background())
		if err == nil {
			t.Fatal("expired token must propagate refresh error")
		}
	})
}

func TestAuthorization_proactiveRefreshFails_backsOffUntilRetryTime(t *testing.T) {
	epoch := clock.NewFake().Now()
	f := newProviderFixture(t, providerSetup{Token: storedToken(epoch.Add(time.Minute))})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)

	authorize := func() {
		t.Helper()
		got, err := f.provider.Authorization(context.Background())
		if err != nil || got != "Bearer stored-access" {
			t.Fatalf("Authorization = %q, %v; want current token served", got, err)
		}
	}
	authorize()
	hitsAfterFirst := f.endpoint.Hits.Load()
	authorize()
	authorize()
	if hits := f.endpoint.Hits.Load(); hits != hitsAfterFirst {
		t.Errorf("endpoint hits = %d, want %d: calls inside the backoff must not retry", hits, hitsAfterFirst)
	}

	f.clock.Advance(provider.ProactiveRefreshBackoff + time.Second)
	authorize()
	if hits := f.endpoint.Hits.Load(); hits != 2*hitsAfterFirst {
		t.Errorf("endpoint hits = %d, want %d once the backoff elapses", hits, 2*hitsAfterFirst)
	}
}

func TestAuthorization_tokenExpiresDuringBackoff_refreshesImmediately(t *testing.T) {
	epoch := clock.NewFake().Now()
	f := newProviderFixture(t, providerSetup{Token: storedToken(epoch.Add(provider.ProactiveRefreshBackoff / 2))})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)

	if _, err := f.provider.Authorization(context.Background()); err != nil {
		t.Fatalf("first call (sets backoff): %v", err)
	}

	f.endpoint.Status.Store(http.StatusOK)
	f.clock.Advance(provider.ProactiveRefreshBackoff * 3 / 4)

	got, err := f.provider.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after token expires during backoff: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("Authorization = %q, want Bearer new-access (refreshed after expiry)", got)
	}
}

func TestRefreshAuthorization_duringBackoff_stillRefreshesOn401(t *testing.T) {
	epoch := clock.NewFake().Now()
	f := newProviderFixture(t, providerSetup{Token: storedToken(epoch.Add(time.Minute))})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)

	if _, err := f.provider.Authorization(context.Background()); err != nil {
		t.Fatalf("first call (sets backoff): %v", err)
	}
	hitsAfterBackoffSet := f.endpoint.Hits.Load()

	f.endpoint.Status.Store(http.StatusOK)
	got, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization during backoff: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("RefreshAuthorization = %q, want Bearer new-access", got)
	}
	if hits := f.endpoint.Hits.Load(); hits <= hitsAfterBackoffSet {
		t.Errorf("endpoint hits = %d, want > %d: 401 path must bypass backoff", hits, hitsAfterBackoffSet)
	}
}

func TestAuthorization_newerStoredTokenDuringBackoff_refreshesWithoutWaiting(t *testing.T) {
	epoch := clock.NewFake().Now()
	f := newProviderFixture(t, providerSetup{Token: storedToken(epoch.Add(time.Minute))})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)

	if _, err := f.provider.Authorization(context.Background()); err != nil {
		t.Fatalf("first call (sets backoff): %v", err)
	}
	hitsAfterFail := f.endpoint.Hits.Load()
	if _, err := f.provider.Authorization(context.Background()); err != nil {
		t.Fatalf("second call (backoff active): %v", err)
	}
	if hits := f.endpoint.Hits.Load(); hits != hitsAfterFail {
		t.Errorf("endpoint hits = %d, want %d: backoff should suppress retry", hits, hitsAfterFail)
	}

	newer := &oauth2.Token{AccessToken: "newer-access", RefreshToken: "newer-refresh", Expiry: epoch.Add(3 * time.Minute)}
	if err := auth.Save(f.dir, "srv", newer); err != nil {
		t.Fatal(err)
	}
	f.endpoint.Status.Store(http.StatusOK)
	got, err := f.provider.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after token adoption: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("Authorization = %q, want Bearer new-access (backoff cleared by adoption)", got)
	}
	if hits := f.endpoint.Hits.Load(); hits <= hitsAfterFail {
		t.Errorf("endpoint hits = %d, want > %d: should refresh after backoff cleared", hits, hitsAfterFail)
	}
}

func TestAuthorization_newerStoredTokenInWindow_usedWithoutRefreshing(t *testing.T) {
	clk := clock.NewFake()
	dir := t.TempDir()
	t1 := &oauth2.Token{AccessToken: "t1-access", RefreshToken: "t1-refresh", Expiry: clk.Now().Add(10 * time.Minute)}
	if err := auth.Save(dir, "srv", t1); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk.Advance(6 * time.Minute)
	t2 := &oauth2.Token{AccessToken: "t2-access", RefreshToken: "t2-refresh", Expiry: clk.Now().Add(30 * time.Minute)}
	if err := auth.Save(dir, "srv", t2); err != nil {
		t.Fatal(err)
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer t2-access" {
		t.Errorf("Authorization = %q, want Bearer t2-access (disk reload)", got)
	}
	if endpoint.Hits.Load() != 0 {
		t.Errorf("endpoint hits = %d, want 0 (disk reload avoids refresh)", endpoint.Hits.Load())
	}
}

func TestAuthorization_browserLoginDuringBackoff_refreshesWithoutWaiting(t *testing.T) {
	epoch := clock.NewFake().Now()
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(epoch.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	params := provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: mock.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFakeAt(epoch),
	}
	registry := provider.NewRegistry()
	prov, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	mock.Status.Store(http.StatusServiceUnavailable)
	if _, err := prov.Authorization(context.Background()); err != nil {
		t.Fatalf("first call (sets backoff): %v", err)
	}
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh", Expiry: epoch.Add(time.Minute)}
	if err := registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatal(err)
	}
	mock.Status.Store(http.StatusOK)
	hitsBefore := mock.Hits.Load()
	if _, err := prov.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization after browser login: %v", err)
	}
	if mock.Hits.Load() == hitsBefore {
		t.Error("browser login must clear the backoff so an in-window token refreshes")
	}
}

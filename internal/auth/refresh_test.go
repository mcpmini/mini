//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRemedyError_wrapsErrReauthRequired(t *testing.T) {
	t.Run("missing token names sentinel", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{})
		_, err := f.provider.Authorization(context.Background())
		if err == nil {
			t.Fatal("expected error for missing token")
		}
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("remedyError must wrap ErrReauthRequired; got: %v", err)
		}
	})
	t.Run("refresh failure names sentinel", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
		f.endpoint.status.Store(http.StatusUnauthorized)
		_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		if err == nil {
			t.Fatal("expected refresh failure")
		}
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("remedyError must wrap ErrReauthRequired; got: %v", err)
		}
	})
}

func newProviderAt(t *testing.T, tokenURL string, tok *oauth2.Token) (transport.AuthorizationProvider, *clock.Fake) {
	t.Helper()
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", tok); err != nil {
		t.Fatal(err)
	}
	ac := &config.AuthConfig{
		Type:                    config.AuthTypeOAuth2,
		ClientID:                "cid",
		TokenEndpointAuthMethod: "client_secret_post",
		TokenURL:                tokenURL,
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	return p, clk
}

func refreshToken() *oauth2.Token {
	return &oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh"}
}

func advanceForBackoffs(t *testing.T, clk *clock.Fake, steps []time.Duration) {
	t.Helper()
	for _, d := range steps {
		if err := clk.BlockUntilContext(t.Context(), 1); err != nil {
			t.Fatalf("waiting for backoff timer: %v", err)
		}
		clk.Advance(d)
	}
}

func TestProviderRefresh_429RetriesAndSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "new-access", "refresh_token": "rotated-refresh",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, refreshToken())
	got, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("got %q, want Bearer new-access", got)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("token endpoint hits = %d, want 2 (one 429 then success)", n)
	}
}

func TestProviderRefresh_503RetriesAndSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "new-access", "refresh_token": "rotated-refresh",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, refreshToken())
	got, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("got %q, want Bearer new-access", got)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("token endpoint hits = %d, want 2 (one 503 then success)", n)
	}
}

func TestProviderRefresh_transientBudgetExhausted(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	p, clk := newProviderAt(t, srv.URL, refreshToken())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
		errCh <- err
	}()
	advanceForBackoffs(t, clk, []time.Duration{time.Second, 2 * time.Second})

	err := <-errCh
	if err == nil {
		t.Fatal("expected error after budget exhaustion")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("transient 503 must not produce ErrReauthRequired: %v", err)
	}
	if n := hits.Load(); n != 3 {
		t.Errorf("token endpoint hits = %d, want 3 (all attempts exhausted)", n)
	}
}

func TestProviderRefresh_networkFailureIsTransient(t *testing.T) {
	p, clk := newProviderAt(t, "http://127.0.0.1:1/token", refreshToken())
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(ctx, "Bearer stored-access")
		errCh <- err
	}()

	if err := clk.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	cancel()

	err := <-errCh
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("network failure must not produce ErrReauthRequired: %v", err)
	}
}

func TestProviderRefresh_invalidGrantRequiresReauth(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error":             "invalid_grant",
			"error_description": "token expired or revoked",
		})
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, refreshToken())
	_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected error for invalid_grant")
	}
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("invalid_grant must produce ErrReauthRequired: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (no retry on reauth error)", n)
	}
}

func TestProviderRefresh_401RequiresReauth(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, refreshToken())
	_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("HTTP 401 from token endpoint must produce ErrReauthRequired: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (no retry on reauth)", n)
	}
}

func TestProviderRefresh_contextCancellationStopsBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	p, clk := newProviderAt(t, srv.URL, refreshToken())
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(ctx, "Bearer stored-access")
		errCh <- err
	}()

	if err := clk.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled after cancellation, got: %v", err)
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("context cancellation must not produce ErrReauthRequired")
	}
}

func TestProviderRefresh_retryAfterDeltaAndDate(t *testing.T) {
	epoch := clock.NewFake().Now()
	cases := []struct {
		name        string
		retryHeader string
		advanceFor  time.Duration
	}{
		{"delta 5s", "5", 5 * time.Second},
		{"delta capped at 60s", "70", 60 * time.Second},
		{"missing header uses 1s backoff", "", time.Second},
		{"http-date 10s", epoch.Add(10 * time.Second).UTC().Format(http.TimeFormat), 10 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 1 {
					w.Header().Set("Retry-After", tc.retryHeader)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"access_token": "new-access", "refresh_token": "rotated-refresh",
					"token_type": "Bearer", "expires_in": 3600,
				})
			}))
			t.Cleanup(srv.Close)

			p, clk := newProviderAt(t, srv.URL, refreshToken())
			errCh := make(chan error, 1)
			go func() {
				_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
				errCh <- err
			}()

			advanceForBackoffs(t, clk, []time.Duration{tc.advanceFor})
			if err := <-errCh; err != nil {
				t.Fatalf("RefreshAuthorization: %v", err)
			}
			if n := hits.Load(); n != 2 {
				t.Errorf("hits = %d, want 2", n)
			}
		})
	}
}

func TestProviderRefresh_missingRefreshTokenRequiresReauthWithoutRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, &oauth2.Token{AccessToken: "stored-access"})
	_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("missing refresh token must require reauthorization, got: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("token endpoint hits = %d, want 0", n)
	}
}

func TestProviderRefresh_malformedSuccessIsTerminalWithoutRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"token_type":"Bearer"}`) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	p, _ := newProviderAt(t, srv.URL, refreshToken())
	_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected malformed token response to fail")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("malformed token response must be terminal, not reauthorization: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (terminal errors are not retried)", n)
	}
}

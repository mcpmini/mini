//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProviderAuthorization_expiryBoundary(t *testing.T) {
	epoch := clock.NewFake().Now()
	shortLived := storedToken(epoch.Add(30 * time.Second))
	shortLived.ExpiresIn = 30
	cases := []struct {
		name        string
		token       *oauth2.Token
		wantHeader  string
		wantRefresh int32
	}{
		{"before skew window keeps stored token", storedToken(epoch.Add(10 * time.Minute)), "Bearer stored-access", 0},
		{"exactly at expiry minus skew refreshes", storedToken(epoch.Add(2 * time.Minute)), "Bearer new-access", 1},
		{"inside skew window refreshes", storedToken(epoch.Add(time.Minute)), "Bearer new-access", 1},
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

func TestProviderAuthorization_discoversMissingTokenEndpoint(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	endpoint := newTokenEndpoint(t)
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint":           "https://as.example/authorize",
			"token_endpoint":                   endpoint.srv.URL,
			"code_challenge_methods_supported": []string{"S256"},
		}) //nolint:errcheck
	}))
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  discovery.URL + "/mcp",
		Clock:      clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if endpoint.hits.Load() != 1 {
		t.Fatalf("token endpoint hits = %d, want 1", endpoint.hits.Load())
	}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.lastResource != discovery.URL+"/mcp" {
		t.Errorf("resource = %q, want %q", endpoint.lastResource, discovery.URL+"/mcp")
	}
}

func TestProviderAuthorization_discoveryFailureIsTransient(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(failServer.Close)

	clk := clock.NewFake()
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  failServer.URL + "/mcp",
		Clock:      clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Authorization(context.Background())
		errCh <- err
	}()
	advanceForBackoffs(t, clk, []time.Duration{time.Second, 2 * time.Second})
	if err := <-errCh; err == nil {
		t.Fatal("expected discovery failure")
	} else if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("transient discovery failure must not require reauthorization: %v", err)
	}
}

func TestProviderAuthorization_missingTokenNamesRemedy(t *testing.T) {
	f := newProviderFixture(t, providerSetup{})
	_, err := f.provider.Authorization(context.Background())
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "mini auth srv") {
		t.Errorf("error should name remedy, got: %v", err)
	}
}

func TestProviderAuthorization_concurrentCallsSingleRefresh(t *testing.T) {
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

type prmDiscoveryFixture struct {
	srv     *httptest.Server
	prmHits atomic.Int32
}

func newPRMDiscoveryFixture(t *testing.T, tokenURL string) *prmDiscoveryFixture {
	t.Helper()
	f := &prmDiscoveryFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/prm"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/prm":
			if f.prmHits.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{f.srv.URL}}) //nolint:errcheck
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   tokenURL,
				"code_challenge_methods_supported": []string{"S256"},
			}) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestProviderAuthorization_lazyDiscoveryRetriesTransientPRM(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	endpoint := newTokenEndpoint(t)
	discovery := newPRMDiscoveryFixture(t, endpoint.srv.URL)
	clk := clock.NewFake()
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir, ServerName: "srv", ServerURL: discovery.srv.URL + "/mcp", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { _, err := p.Authorization(context.Background()); errCh <- err }()
	advanceForBackoffs(t, clk, []time.Duration{time.Second})
	if err := <-errCh; err != nil {
		t.Fatalf("Authorization with transient PRM failure: %v", err)
	}
	if got := discovery.prmHits.Load(); got != 2 {
		t.Errorf("PRM hits = %d, want 2", got)
	}
	if got := endpoint.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1", got)
	}
}

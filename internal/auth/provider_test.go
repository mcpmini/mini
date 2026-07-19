//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

type tokenEndpoint struct {
	srv           *httptest.Server
	hits          atomic.Int32
	status        atomic.Int32
	mu            sync.Mutex
	accessToken   string
	refreshToken  string
	lastGrant     string
	lastRefresh   string
	lastBasicAuth string
	lastClientID  string
}

func newTokenEndpoint(t *testing.T) *tokenEndpoint {
	t.Helper()
	e := &tokenEndpoint{accessToken: "new-access", refreshToken: "rotated-refresh"}
	e.status.Store(http.StatusOK)
	e.srv = httptest.NewServer(http.HandlerFunc(e.handle))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *tokenEndpoint) handle(w http.ResponseWriter, r *http.Request) {
	e.hits.Add(1)
	if status := int(e.status.Load()); status != http.StatusOK {
		http.Error(w, "refresh rejected", status)
		return
	}
	r.ParseForm() //nolint:errcheck
	e.mu.Lock()
	e.lastGrant = r.FormValue("grant_type")
	e.lastRefresh = r.FormValue("refresh_token")
	e.lastClientID = r.FormValue("client_id")
	user, _, ok := r.BasicAuth()
	if ok {
		e.lastBasicAuth = user
	}
	access, refresh := e.accessToken, e.refreshToken
	e.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token": access, "refresh_token": refresh,
		"token_type": "Bearer", "expires_in": 3600,
	})
}

type providerFixture struct {
	dir      string
	endpoint *tokenEndpoint
	clock    *clock.Fake
	provider transport.AuthorizationProvider
}

type providerSetup struct {
	Token *oauth2.Token
	Auth  *config.AuthConfig
}

func newProviderFixture(t *testing.T, s providerSetup) *providerFixture {
	t.Helper()
	f := &providerFixture{dir: t.TempDir(), endpoint: newTokenEndpoint(t), clock: clock.NewFake()}
	if s.Auth == nil {
		s.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"}
	}
	s.Auth.TokenURL = f.endpoint.srv.URL
	if s.Token != nil {
		if err := auth.Save(f.dir, "srv", s.Token); err != nil {
			t.Fatal(err)
		}
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: s.Auth, ConfigDir: f.dir, ServerName: "srv", Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	f.provider = p
	return f
}

func storedToken(expiry time.Time) *oauth2.Token {
	return &oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: expiry}
}

func TestProviderAuthorization_expiryBoundary(t *testing.T) {
	epoch := clock.NewFake().Now()
	cases := []struct {
		name        string
		expiry      time.Time
		wantHeader  string
		wantRefresh int32
	}{
		{"before skew window keeps stored token", epoch.Add(10 * time.Minute), "Bearer stored-access", 0},
		{"exactly at expiry minus skew refreshes", epoch.Add(2 * time.Minute), "Bearer new-access", 1},
		{"inside skew window refreshes", epoch.Add(time.Minute), "Bearer new-access", 1},
		{"already expired refreshes", epoch.Add(-time.Hour), "Bearer new-access", 1},
		{"zero expiry never refreshes proactively", time.Time{}, "Bearer stored-access", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProviderFixture(t, providerSetup{Token: storedToken(tc.expiry)})
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

func TestProviderRefresh_persistsRotatedRefreshToken(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
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

func TestNewProvider_appliesConfidentialClientRegistration(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "dcr-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	endpoint := newTokenEndpoint(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if endpoint.lastBasicAuth != "dcr-client" {
		t.Errorf("refresh must authenticate with the registered confidential client, basic user = %q", endpoint.lastBasicAuth)
	}
}

func TestNewProvider_inconsistentRegistrationErrors(t *testing.T) {
	t.Run("ignored when explicit client_id set", func(t *testing.T) {
		dir := t.TempDir()
		reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "orphan-secret", TokenEndpointAuthMethod: "none"}
		if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
			t.Fatal(err)
		}
		ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"}
		if _, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err != nil {
			t.Fatalf("inconsistent registration must be ignored when explicit client_id is set: %v", err)
		}
	})
	t.Run("errors when no explicit client_id", func(t *testing.T) {
		dir := t.TempDir()
		reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "orphan-secret", TokenEndpointAuthMethod: "none"}
		if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
			t.Fatal(err)
		}
		ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"}
		if _, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err == nil {
			t.Fatal("expected construction error for inconsistent registration when no explicit client_id")
		}
	})
}

func TestNewProvider_missingRegistrationIsPublicClient(t *testing.T) {
	dir := t.TempDir()
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"}
	if _, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err != nil {
		t.Fatalf("missing registration must not error: %v", err)
	}
}

func TestNewProvider_concurrentConstructionNoRace(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", TokenEndpointAuthMethod: "none"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	shared := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := auth.NewProvider(auth.ProviderParams{
				AuthConfig: shared, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake(),
			})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Authorization(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestNewProvider_explicitClientIDNotOverriddenByRegistration(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "stale-id", ClientSecret: "stale-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	endpoint := newTokenEndpoint(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "manual-id", TokenURL: endpoint.srv.URL}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.mu.Lock()
	gotClientID := endpoint.lastClientID
	gotBasicUser := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	// The oauth2 library sends client_id via basic-auth header or form body depending
	// on auth style auto-detection; check both locations so the assertion is not
	// sensitive to the internal detection order.
	usedManualID := gotClientID == "manual-id" || gotBasicUser == "manual-id"
	if !usedManualID {
		t.Errorf("manual-id must be used; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
	}
	if gotClientID == "stale-id" || gotBasicUser == "stale-id" {
		t.Errorf("stale DCR registration must not override explicit client_id; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
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

func TestProviderAuthorization_lazyDiscovery_success(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()

	// Combined server: discovery at /.well-known/oauth-authorization-server
	// points to the httptest token endpoint (loopback — requires UseLoopbackEndpoints).
	discoverySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_endpoint":           "https://as.example.com/authorize",
			"token_endpoint":                   endpoint.srv.URL,
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	t.Cleanup(discoverySrv.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}

	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  discoverySrv.URL + "/mcp",
		Clock:      clk,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization with lazy discovery: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("got %q, want Bearer new-access", got)
	}
	if hits := endpoint.hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (discovery populated TokenURL)", hits)
	}
}

func TestProviderAuthorization_lazyDiscovery_discoveryFailure(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(failSrv.Close)

	clk := clock.NewFake()
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}

	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  failSrv.URL + "/mcp",
		Clock:      clk,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Authorization(context.Background())
	if err == nil {
		t.Fatal("expected error when endpoint discovery fails")
	}
	if !strings.Contains(err.Error(), "mini auth srv") {
		t.Errorf("error must name remedy command, got: %v", err)
	}
}

func TestProviderCache_sharedAcrossGetOrCreate(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()
	dir := t.TempDir()

	if err := auth.Save(dir, "srv", storedToken(clk.Now())); err != nil {
		t.Fatal(err)
	}

	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()

	providers := make([]transport.AuthorizationProvider, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := cache.GetOrCreate(params)
			if err != nil {
				t.Error(err)
				return
			}
			providers[i] = p
		}(i)
	}
	wg.Wait()

	if providers[0] != providers[1] {
		t.Error("concurrent GetOrCreate must return the same provider instance")
	}

	stale := "Bearer stored-access"
	tokens := make([]string, 2)
	var mu sync.Mutex
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := providers[i].RefreshAuthorization(context.Background(), stale)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			tokens[i] = v
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i, tok := range tokens {
		if tok != "Bearer new-access" {
			t.Errorf("providers[%d] got %q, want Bearer new-access", i, tok)
		}
	}
	if hits := endpoint.hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1 (shared provider single-flights refresh)", hits)
	}
}

func TestProviderCache_evictionYieldsFreshProvider(t *testing.T) {
	clk := clock.NewFake()
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}

	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()

	p1, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	cache.Evict("srv")

	p2, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	if p1 == p2 {
		t.Error("after Evict, GetOrCreate must return a fresh provider instance")
	}
}

//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestProviderRefresh_cimdClientIDSurvivesLazyDiscoveryAndTokenAdoption(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	var lastClientID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"authorization_endpoint":                "https://as.example.com/authorize",
				"token_endpoint":                        "http://" + r.Host + "/token",
				"code_challenge_methods_supported":      []string{"S256"},
				"client_id_metadata_document_supported": true,
			})
		case "/token":
			r.ParseForm() //nolint:errcheck
			cid := r.FormValue("client_id")
			if cid == "" {
				// oauth2 AuthStyleInHeader sends url.QueryEscape(client_id) as Basic Auth user.
				if user, _, ok := r.BasicAuth(); ok {
					if decoded, err := url.QueryUnescape(user); err == nil {
						cid = decoded
					}
				}
			}
			lastClientID = cid
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "new-access", "refresh_token": "new-refresh",
				"token_type": "Bearer", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv",
		ServerURL: srv.URL + "/mcp",
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.RefreshAuthorization(context.Background(), "Bearer old-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if lastClientID != auth.ClientMetadataURL {
		t.Errorf("client_id = %q, want %q", lastClientID, auth.ClientMetadataURL)
	}

	if err := auth.Save(dir, "srv", &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}); err != nil {
		t.Fatal(err)
	}
	lastClientID = ""
	for _, stale := range []string{"Bearer new-access", "Bearer external-access"} {
		if _, err := p.RefreshAuthorization(context.Background(), stale); err != nil {
			t.Fatalf("RefreshAuthorization(%s): %v", stale, err)
		}
	}
	if lastClientID != auth.ClientMetadataURL {
		t.Errorf("after adopting an external token: client_id = %q, want %q", lastClientID, auth.ClientMetadataURL)
	}
}

func TestProviderRefresh_cancelledCallerPreservesRotatedToken(t *testing.T) {
	endpointReceived := make(chan struct{})
	releaseEndpoint := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		close(endpointReceived)
		<-releaseEndpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "rotated-access", "refresh_token": "rotated-refresh",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: srv.URL},
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
	<-endpointReceived
	cancel()
	close(releaseEndpoint)

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
	if hits.Load() != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (rotated token reused)", hits.Load())
	}
}

func TestProvider_reloadAdoptsRegistrationForCredentials(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()

	initialTok := &oauth2.Token{
		AccessToken: "initial-access", RefreshToken: "initial-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	if err := auth.Save(dir, "srv", initialTok); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "dcr-client"}); err != nil {
		t.Fatal(err)
	}
	freshTok := &oauth2.Token{
		AccessToken: "auth-access", RefreshToken: "auth-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	if err := auth.Save(dir, "srv", freshTok); err != nil {
		t.Fatal(err)
	}

	// Trigger adoption via 401-style stale check; must not hit the token endpoint.
	got, err := p.RefreshAuthorization(context.Background(), "Bearer initial-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer auth-access" {
		t.Fatalf("expected adopted token, got %q", got)
	}
	if endpoint.hits.Load() != 0 {
		t.Fatalf("token endpoint hit during adoption, want 0 hits")
	}

	// Advance clock past the adopted token's expiry; refresh must use the adopted registration.
	clk.Advance(2 * time.Hour)
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization after expiry: %v", err)
	}

	endpoint.mu.Lock()
	clientIDForm, clientIDBasic := endpoint.lastClientID, endpoint.lastBasicAuth
	endpoint.mu.Unlock()

	if clientIDForm != "dcr-client" && clientIDBasic != "dcr-client" {
		t.Errorf("token endpoint client_id = form:%q basic:%q, want dcr-client", clientIDForm, clientIDBasic)
	}
}

//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type authObservation struct {
	authorizationHeader string
	formClientID        string
	formClientSecret    string
}

type capturingTokenServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	exchange authObservation
	refresh  authObservation
}

func newCapturingTokenServer(t *testing.T) *capturingTokenServer {
	t.Helper()
	c := &capturingTokenServer{}
	c.srv = httptest.NewServer(http.HandlerFunc(c.handle))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capturingTokenServer) handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	obs := authObservation{
		authorizationHeader: r.Header.Get("Authorization"),
		formClientID:        r.PostFormValue("client_id"),
		formClientSecret:    r.PostFormValue("client_secret"),
	}
	c.mu.Lock()
	if r.PostFormValue("grant_type") == "refresh_token" {
		c.refresh = obs
	} else {
		c.exchange = obs
	}
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token":  "access-" + r.PostFormValue("grant_type"),
		"refresh_token": "refresh-tok",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func hydrateFromSavedRegistration(t *testing.T, reg *auth.Registration, tokenURL string, clk clock.Clock) *config.AuthConfig {
	t.Helper()
	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatalf("SaveRegistration: %v", err)
	}
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	t.Cleanup(asSrv.Close)

	sc := &config.ServerConfig{
		URL: asSrv.URL + "/mcp",
		// pre-set AuthURL/TokenURL so discovery never overwrites them — a loopback TokenURL
		// from discovery metadata would fail validateEndpointURL
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			AuthURL:  "https://as.example.com/authorize",
			TokenURL: tokenURL,
		},
	}
	params := auth.ResolveEndpointsParams{ConfigDir: dir, ServerName: "srv", Clock: clk}
	if err := auth.ResolveEndpoints(context.Background(), sc, params); err != nil {
		t.Fatalf("ResolveEndpoints: %v", err)
	}
	return sc.Auth
}

func exchangeAndRefresh(t *testing.T, ac *config.AuthConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authURL, doneCh, err := auth.StartPKCEFlow(ctx, ac)
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	if err := simulateBrowser(authURL); err != nil {
		t.Fatalf("simulateBrowser: %v", err)
	}
	result := <-doneCh
	if result.Err != nil {
		t.Fatalf("PKCE exchange: %v", result.Err)
	}
	// backdated so Refresh actually hits the token endpoint rather than returning the cached token
	result.Token.Expiry = time.Now().Add(-time.Hour)
	if _, err := auth.Refresh(context.Background(), ac, result.Token); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

func assertBasicAuth(t *testing.T, label string, obs authObservation, clientID, clientSecret string) {
	t.Helper()
	want := "Basic " + basicAuthValue(clientID, clientSecret)
	if obs.authorizationHeader != want {
		t.Errorf("%s: Authorization header = %q, want %q", label, obs.authorizationHeader, want)
	}
	if obs.formClientSecret != "" {
		t.Errorf("%s: client_secret leaked into form body: %q", label, obs.formClientSecret)
	}
}

func basicAuthValue(clientID, clientSecret string) string {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.SetBasicAuth(clientID, clientSecret)
	return strings.TrimPrefix(req.Header.Get("Authorization"), "Basic ")
}

func assertPostAuth(t *testing.T, label string, obs authObservation, clientID, clientSecret string) {
	t.Helper()
	if obs.formClientSecret != clientSecret {
		t.Errorf("%s: form client_secret = %q, want %q", label, obs.formClientSecret, clientSecret)
	}
	if obs.formClientID != clientID {
		t.Errorf("%s: form client_id = %q, want %q", label, obs.formClientID, clientID)
	}
	if obs.authorizationHeader != "" {
		t.Errorf("%s: unexpected Authorization header for client_secret_post: %q", label, obs.authorizationHeader)
	}
}

func TestHydratedRegistration_clientSecretBasicAuthenticatesExchangeAndRefresh(t *testing.T) {
	tokenSrv := newCapturingTokenServer(t)
	reg := &auth.Registration{
		ClientID:                "hydrated-basic-client",
		ClientSecret:            "sk-test-usurp-basic",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	ac := hydrateFromSavedRegistration(t, reg, tokenSrv.srv.URL, clock.System())
	if ac.ClientSecret != reg.ClientSecret || ac.TokenEndpointAuthMethod != reg.TokenEndpointAuthMethod {
		t.Fatalf("hydration did not copy registration fields onto AuthConfig: %+v", ac)
	}

	exchangeAndRefresh(t, ac)

	assertBasicAuth(t, "exchange", tokenSrv.exchange, reg.ClientID, reg.ClientSecret)
	assertBasicAuth(t, "refresh", tokenSrv.refresh, reg.ClientID, reg.ClientSecret)
}

func TestHydratedRegistration_clientSecretPostAuthenticatesExchangeAndRefresh(t *testing.T) {
	tokenSrv := newCapturingTokenServer(t)
	reg := &auth.Registration{
		ClientID:                "hydrated-post-client",
		ClientSecret:            "sk-test-usurp-post",
		TokenEndpointAuthMethod: "client_secret_post",
	}
	ac := hydrateFromSavedRegistration(t, reg, tokenSrv.srv.URL, clock.System())

	exchangeAndRefresh(t, ac)

	assertPostAuth(t, "exchange", tokenSrv.exchange, reg.ClientID, reg.ClientSecret)
	assertPostAuth(t, "refresh", tokenSrv.refresh, reg.ClientID, reg.ClientSecret)
}

func TestHydratedRegistration_noSecretLeavesPublicClientUnchanged(t *testing.T) {
	tokenSrv := newCapturingTokenServer(t)
	reg := &auth.Registration{ClientID: "public-client"}
	ac := hydrateFromSavedRegistration(t, reg, tokenSrv.srv.URL, clock.System())

	if ac.ClientSecret != "" || ac.TokenEndpointAuthMethod != "" {
		t.Fatalf("expected no secret/method hydrated for public client, got secret=%q method=%q", ac.ClientSecret, ac.TokenEndpointAuthMethod)
	}

	exchangeAndRefresh(t, ac)

	if tokenSrv.exchange.formClientSecret != "" || tokenSrv.refresh.formClientSecret != "" {
		t.Error("client_secret sent for a public client registration")
	}
}

func TestHydratedRegistration_olderFileWithoutNewFieldsLoadsFine(t *testing.T) {
	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "legacy-client"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := auth.LoadRegistration(dir, "srv")
	if err != nil {
		t.Fatalf("LoadRegistration: %v", err)
	}
	if loaded.ClientID != "legacy-client" {
		t.Errorf("ClientID = %q, want legacy-client", loaded.ClientID)
	}
	if loaded.ClientSecret != "" || loaded.TokenEndpointAuthMethod != "" || loaded.ClientSecretExpiresAt != 0 {
		t.Errorf("expected zero-value new fields for legacy file, got %+v", loaded)
	}
}

func TestRegistrationExpiry_appliesOrIgnoresSecretByBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		expiresAt   int64
		wantApplied bool
	}{
		{"zero means never expires", 0, true},
		{"future expiry applies secret", base.Add(time.Hour).Unix(), true},
		{"exactly now is treated as expired", base.Unix(), false},
		{"past is treated as expired", base.Add(-time.Hour).Unix(), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &auth.Registration{
				ClientID:                "expiry-client",
				ClientSecret:            "sk-test-usurp-expiry",
				TokenEndpointAuthMethod: "client_secret_basic",
				ClientSecretExpiresAt:   tc.expiresAt,
			}
			ac := hydrateFromSavedRegistration(t, reg, "https://as.example.com/token", clock.NewFakeAt(base))
			if got := ac.ClientSecret != ""; got != tc.wantApplied {
				t.Errorf("secret applied = %v, want %v (ClientSecret=%q)", got, tc.wantApplied, ac.ClientSecret)
			}
			if ac.ClientID != reg.ClientID {
				t.Errorf("ClientID = %q, want %q — ClientID applies regardless of secret expiry", ac.ClientID, reg.ClientID)
			}
		})
	}
}

func TestRegistrationInconsistency_failsResolutionNamingTheField(t *testing.T) {
	tests := []struct {
		name   string
		reg    *auth.Registration
		wantIn string
	}{
		{
			name:   "client_secret_basic with no secret",
			reg:    &auth.Registration{ClientID: "c1", TokenEndpointAuthMethod: "client_secret_basic"},
			wantIn: "client_secret",
		},
		{
			name:   "client_secret_post with no secret",
			reg:    &auth.Registration{ClientID: "c2", TokenEndpointAuthMethod: "client_secret_post"},
			wantIn: "client_secret",
		},
		{
			name:   "secret with method absent",
			reg:    &auth.Registration{ClientID: "c3", ClientSecret: "sk-test-usurp-inconsistent"},
			wantIn: "token_endpoint_auth_method",
		},
		{
			name:   "secret with method none",
			reg:    &auth.Registration{ClientID: "c4", ClientSecret: "sk-test-usurp-inconsistent", TokenEndpointAuthMethod: "none"},
			wantIn: "token_endpoint_auth_method",
		},
		{
			name:   "unrecognized method",
			reg:    &auth.Registration{ClientID: "c5", ClientSecret: "sk-test-usurp-inconsistent", TokenEndpointAuthMethod: "client_secret_jwt"},
			wantIn: "client_secret_jwt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := auth.SaveRegistration(dir, "srv", tc.reg); err != nil {
				t.Fatal(err)
			}
			asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   "https://as.example.com/token",
				"code_challenge_methods_supported": []string{"S256"},
			})
			defer asSrv.Close()

			sc := &config.ServerConfig{URL: asSrv.URL + "/mcp", Auth: &config.AuthConfig{Type: "oauth2"}}
			params := auth.ResolveEndpointsParams{ConfigDir: dir, ServerName: "srv", Clock: clock.System()}
			err := auth.ResolveEndpoints(context.Background(), sc, params)
			if err == nil {
				t.Fatal("expected resolution error for inconsistent registration metadata")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %q, want it to name %q", err.Error(), tc.wantIn)
			}
			if tc.reg.ClientSecret != "" && strings.Contains(err.Error(), tc.reg.ClientSecret) {
				t.Errorf("error leaked the client_secret value: %q", err.Error())
			}
		})
	}
}

func TestResolveEndpoints_freshDCRCapturesAndPersistsConfidentialClient(t *testing.T) {
	dir := t.TempDir()
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"client_id":                  "fresh-confidential-id",
			"client_secret":              "sk-test-usurp-fresh",
			"token_endpoint_auth_method": "client_secret_post",
		})
	}))
	defer regSrv.Close()

	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"registration_endpoint":            regSrv.URL + "/register",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{URL: asSrv.URL + "/mcp", Auth: &config.AuthConfig{Type: "oauth2"}}
	params := auth.ResolveEndpointsParams{ConfigDir: dir, ServerName: "srv", Clock: clock.System()}
	if err := auth.ResolveEndpoints(context.Background(), sc, params); err != nil {
		t.Fatalf("ResolveEndpoints: %v", err)
	}
	if sc.Auth.ClientSecret != "sk-test-usurp-fresh" || sc.Auth.TokenEndpointAuthMethod != "client_secret_post" {
		t.Errorf("AuthConfig not hydrated from fresh DCR response: secret=%q method=%q", sc.Auth.ClientSecret, sc.Auth.TokenEndpointAuthMethod)
	}

	persisted, err := auth.LoadRegistration(dir, "srv")
	if err != nil {
		t.Fatalf("LoadRegistration: %v", err)
	}
	if persisted.ClientSecret != "sk-test-usurp-fresh" || persisted.TokenEndpointAuthMethod != "client_secret_post" {
		t.Errorf("persisted registration missing confidential-client fields: %+v", persisted)
	}
}

func TestClientSecretNeverAppearsInResolutionOrExchangeErrors(t *testing.T) {
	const secret = "sk-test-usurp-should-not-leak"
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "leak-check-client", ClientSecret: secret, TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	rejectingTokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_client", http.StatusUnauthorized)
	}))
	defer rejectingTokenSrv.Close()

	ac := hydrateFromSavedRegistration(t, reg, rejectingTokenSrv.URL, clock.System())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authURL, doneCh, err := auth.StartPKCEFlow(ctx, ac)
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	if err := simulateBrowser(authURL); err != nil {
		t.Fatalf("simulateBrowser: %v", err)
	}
	result := <-doneCh
	if result.Err == nil {
		t.Fatal("expected exchange against a rejecting token endpoint to fail")
	}
	if strings.Contains(result.Err.Error(), secret) {
		t.Errorf("exchange error leaked the client_secret: %q", result.Err.Error())
	}
}

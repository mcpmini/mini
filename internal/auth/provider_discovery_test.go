//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

// resolvedClientID returns the client_id sent to the token endpoint, checking
// both the form body (InParams) and Basic auth header (InHeader), since oauth2
// AuthStyleAutoDetect tries Basic auth first.
func resolvedClientID(endpoint *mockAuthServer) string {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.lastClientID != "" {
		return endpoint.lastClientID
	}
	decoded, err := url.QueryUnescape(endpoint.lastBasicAuth)
	if err != nil {
		return ""
	}
	return decoded
}

func TestAuthorization_noTokenURLAfterRestart_discoversEndpointAndRefreshes(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "new-access"

	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_endpoint":           "https://as.example/authorize",
			"token_endpoint":                   endpoint.srv.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
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
	lastResource := endpoint.lastResource
	endpoint.mu.Unlock()
	if lastResource != discovery.URL+"/mcp" {
		t.Errorf("resource = %q, want %q", lastResource, discovery.URL+"/mcp")
	}
}

func TestRefreshAuthorization_cimdServerAfterRestart_keepsCIMDClientIDAcrossTokenAdoption(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "new-access"
	endpoint.refreshToken = "new-refresh"

	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_endpoint":                "https://as.example.com/authorize",
			"token_endpoint":                        endpoint.srv.URL + "/token",
			"code_challenge_methods_supported":      []string{"S256"},
			"client_id_metadata_document_supported": true,
		})
	}))
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv",
		ServerURL: discovery.URL + "/mcp",
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.RefreshAuthorization(context.Background(), "Bearer old-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got := resolvedClientID(endpoint); got != auth.ClientMetadataURL {
		t.Errorf("first refresh client_id = %q, want %q", got, auth.ClientMetadataURL)
	}

	// Shut down the discovery server. Subsequent refreshes must not need to
	// re-discover: carryOverLazyDiscovery must have preserved TokenURL and ClientID.
	discovery.Close()

	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "external-access", RefreshToken: "external-refresh",
	}); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"Bearer new-access", "Bearer external-access"} {
		if _, err := p.RefreshAuthorization(context.Background(), stale); err != nil {
			t.Fatalf("RefreshAuthorization(%s): %v", stale, err)
		}
	}
	if got := resolvedClientID(endpoint); got != auth.ClientMetadataURL {
		t.Errorf("after token adoption client_id = %q, want %q", got, auth.ClientMetadataURL)
	}
}

func TestRefreshAuthorization_externalLoginWithNewDCRClient_dropsStaleClientID(t *testing.T) {
	dir := t.TempDir()
	endpoint := newMockAuthServer(t)
	clk := clock.NewFake()

	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "old-dcr-client"}); err != nil {
		t.Fatal(err)
	}
	initialTok := &oauth2.Token{
		AccessToken: "initial-access", RefreshToken: "initial-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	if err := auth.Save(dir, "srv", initialTok); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Remove the stale registration to simulate a fresh login via a different client
	// (e.g. browser PKCE flow, no DCR). The old client ID must not be carried over.
	os.Remove(filepath.Join(dir, "internal", "srv.dcr.json")) //nolint:errcheck

	externalTok := &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}
	if err := auth.Save(dir, "srv", externalTok); err != nil {
		t.Fatal(err)
	}
	got, err := p.RefreshAuthorization(context.Background(), "Bearer initial-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization after adoption: %v", err)
	}
	if got != "Bearer external-access" {
		t.Fatalf("expected external token, got %q", got)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer external-access"); err != nil {
		t.Fatalf("second RefreshAuthorization: %v", err)
	}
	if got := resolvedClientID(endpoint); got == "old-dcr-client" {
		t.Errorf("stale DCR client_id must not survive token adoption, got %q", got)
	}
}

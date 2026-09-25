//go:build test

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func clientIDSentToTokenEndpoint(endpoint *mockAuthServer) string {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.lastClientID != "" {
		return endpoint.lastClientID
	}
	// oauth2's AuthStyleAutoDetect sends client_id via Basic auth first.
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

	discovery := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example/authorize",
		"token_endpoint":                   endpoint.srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
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

	discovery := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        endpoint.srv.URL + "/token",
		"code_challenge_methods_supported":      []string{"S256"},
		"client_id_metadata_document_supported": true,
	})
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
	if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
		t.Errorf("first refresh client_id = %q, want %q", got, auth.ClientMetadataURL)
	}

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
	if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
		t.Errorf("after token adoption client_id = %q, want %q", got, auth.ClientMetadataURL)
	}
}

func TestRefreshAuthorization_externalLoginWithoutRegistration_dropsStaleDCRClientID(t *testing.T) {
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
	if got := clientIDSentToTokenEndpoint(endpoint); got == "old-dcr-client" {
		t.Errorf("stale DCR client_id must not survive token adoption, got %q", got)
	}
}

func TestRefreshAuthorization_discovery500_returnsReauthRemedy(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newMockAuthServer(t)
	tokenPOSTs := endpoint.hits.Load()

	discovery := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   endpoint.srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
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

	discovery.Close()

	_, err = p.RefreshAuthorization(context.Background(), "Bearer access")
	if err == nil {
		t.Fatal("expected error when discovery returns network error, got nil")
	}
	if !strings.Contains(err.Error(), "mini auth") {
		t.Errorf("expected reauth remedy in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "discover") {
		t.Errorf("expected discovery error in message, got %q", err.Error())
	}
	if endpoint.hits.Load() != tokenPOSTs {
		t.Errorf("token endpoint was called %d times after discovery failure, want 0", endpoint.hits.Load()-tokenPOSTs)
	}
}

func TestRefreshAuthorization_noServerURLOrTokenURL_returnsReauthRemedyWithoutNetwork(t *testing.T) {
	endpoint := newMockAuthServer(t)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv",
		Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.RefreshAuthorization(context.Background(), "Bearer access")
	if err == nil {
		t.Fatal("expected error when no server URL and no token URL, got nil")
	}
	if !strings.Contains(err.Error(), "mini auth") {
		t.Errorf("expected reauth remedy in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no server URL") {
		t.Errorf("expected no-server-URL message in error, got %q", err.Error())
	}
	if endpoint.hits.Load() != 0 {
		t.Errorf("token endpoint called %d times, want 0", endpoint.hits.Load())
	}
}

func TestRefreshAuthorization_configuredClientIDOnCIMDServer_keepsConfiguredClientID(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "new-access"

	discovery := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        endpoint.srv.URL + "/token",
		"code_challenge_methods_supported":      []string{"S256"},
		"client_id_metadata_document_supported": true,
	})
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "my-configured-client"},
		ConfigDir:  dir, ServerName: "srv",
		ServerURL: discovery.URL + "/mcp",
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.RefreshAuthorization(context.Background(), "Bearer access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got == auth.ClientMetadataURL {
		t.Errorf("CIMD URL must not override configured client_id, got %q", got)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got != "my-configured-client" {
		t.Errorf("client_id = %q, want %q", got, "my-configured-client")
	}
}

func TestRefreshAuthorization_noCIMDAdvert_doesNotUseCIMDClientID(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "new-access"

	discovery := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   endpoint.srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
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

	if _, err := p.RefreshAuthorization(context.Background(), "Bearer access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got == auth.ClientMetadataURL {
		t.Errorf("CIMD URL must not be set when server does not advertise CIMD, got %q", got)
	}
}

func TestRefreshAuthorization_prm503_doesNotPostRefreshTokenToMCPOrigin(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	var tokenHits atomic.Int32
	mcpOriginTokenCalls := &tokenHits

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"):
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		case r.URL.Path == "/token":
			mcpOriginTokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"new","token_type":"Bearer","expires_in":3600}`)) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mcpSrv.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv",
		ServerURL: mcpSrv.URL + "/mcp",
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.RefreshAuthorization(context.Background(), "Bearer access")
	if err == nil {
		t.Fatal("expected error when PRM returns 503, got nil")
	}
	if mcpOriginTokenCalls.Load() != 0 {
		t.Errorf("refresh token was posted to MCP origin /token %d times, want 0", mcpOriginTokenCalls.Load())
	}
}

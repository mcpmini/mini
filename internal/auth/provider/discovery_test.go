//go:build test

package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func clientIDSentToTokenEndpoint(endpoint *authtest.TokenServer) string {
	endpoint.Mu.Lock()
	defer endpoint.Mu.Unlock()
	if endpoint.LastClientID != "" {
		return endpoint.LastClientID
	}
	// oauth2's AuthStyleAutoDetect sends client_id via Basic auth first.
	decoded, err := url.QueryUnescape(endpoint.LastBasicAuth)
	if err != nil {
		return ""
	}
	return decoded
}

type discoveryFixtureParams struct {
	cimdSupported  bool
	directTokenURL bool
	expiredToken   bool
	clientID       string
	initialDCR     *auth.Registration
}

type discoveryFixture struct {
	dir       string
	serverURL string
	endpoint  *authtest.TokenServer
	clock     *clock.Fake
	provider  transport.AuthorizationProvider
}

func newDiscoveryFixture(t *testing.T, p discoveryFixtureParams) *discoveryFixture {
	t.Helper()
	if !p.directTokenURL {
		auth.UseLoopbackEndpoints()
		t.Cleanup(auth.ResetEndpointValidation)
	}
	f := &discoveryFixture{dir: t.TempDir(), clock: clock.NewFake()}
	f.endpoint = authtest.NewTokenServer(t)
	f.endpoint.AccessToken = "new-access"
	f.endpoint.RefreshToken = "new-refresh"
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: p.clientID}
	pp := provider.Params{ConfigDir: f.dir, ServerName: "srv", Clock: f.clock, AuthConfig: ac}
	if p.directTokenURL {
		ac.TokenURL = f.endpoint.Srv.URL + "/token"
	} else {
		meta := map[string]any{
			"authorization_endpoint":           "https://as.example.com/authorize",
			"token_endpoint":                   f.endpoint.Srv.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		}
		if p.cimdSupported {
			meta["client_id_metadata_document_supported"] = true
		}
		ds := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", meta)
		t.Cleanup(ds.Close)
		pp.ServerURL = ds.URL + "/mcp"
		f.serverURL = ds.URL + "/mcp"
	}
	tok := &oauth2.Token{AccessToken: "old-access", RefreshToken: "old-refresh"}
	if p.expiredToken {
		tok.Expiry = f.clock.Now()
	}
	if err := auth.Save(f.dir, "srv", tok); err != nil {
		t.Fatal(err)
	}
	if p.initialDCR != nil {
		if err := auth.SaveRegistration(f.dir, "srv", p.initialDCR); err != nil {
			t.Fatal(err)
		}
	}
	prov, err := provider.New(pp)
	if err != nil {
		t.Fatal(err)
	}
	f.provider = prov
	return f
}

func TestAuthorization_noTokenURLAfterRestart_discoversEndpointAndRefreshes(t *testing.T) {
	f := newDiscoveryFixture(t, discoveryFixtureParams{clientID: "cid", expiredToken: true})
	if _, err := f.provider.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if f.endpoint.Hits.Load() != 1 {
		t.Fatalf("token endpoint hits = %d, want 1", f.endpoint.Hits.Load())
	}
	f.endpoint.Mu.Lock()
	lastResource := f.endpoint.LastResource
	f.endpoint.Mu.Unlock()
	if lastResource != f.serverURL {
		t.Errorf("resource = %q, want %q", lastResource, f.serverURL)
	}
}

func TestRefreshAuthorization_clientIDAfterRediscovery(t *testing.T) {
	tests := []struct {
		name         string
		fp           discoveryFixtureParams
		afterSetup   func(*testing.T, *discoveryFixture)
		moreCalls    func(*testing.T, *discoveryFixture)
		wantClientID string
		denyClientID string
	}{
		{
			name:         "cimd_server_after_restart_keeps_cimd_client_id_across_token_adoption",
			fp:           discoveryFixtureParams{cimdSupported: true},
			wantClientID: auth.ClientMetadataURL,
			moreCalls: func(t *testing.T, f *discoveryFixture) {
				if got := clientIDSentToTokenEndpoint(f.endpoint); got != auth.ClientMetadataURL {
					t.Errorf("first refresh client_id = %q, want %q", got, auth.ClientMetadataURL)
				}
				auth.Save(f.dir, "srv", &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}) //nolint:errcheck
				for _, stale := range []string{"Bearer new-access", "Bearer external-access"} {
					if _, err := f.provider.RefreshAuthorization(context.Background(), stale); err != nil {
						t.Fatalf("RefreshAuthorization(%s): %v", stale, err)
					}
				}
			},
		},
		{
			name: "external_login_without_registration_drops_stale_dcr_client_id",
			fp: discoveryFixtureParams{
				directTokenURL: true,
				initialDCR:     &auth.Registration{ClientID: "old-dcr-client"},
			},
			afterSetup: func(t *testing.T, f *discoveryFixture) {
				if _, err := f.provider.Authorization(context.Background()); err != nil {
					t.Fatal(err)
				}
				os.Remove(filepath.Join(f.dir, "internal", "srv.dcr.json"))                                              //nolint:errcheck
				auth.Save(f.dir, "srv", &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}) //nolint:errcheck
			},
			moreCalls: func(t *testing.T, f *discoveryFixture) {
				if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer external-access"); err != nil {
					t.Fatalf("second RefreshAuthorization: %v", err)
				}
			},
			denyClientID: "old-dcr-client",
		},
		{
			name:         "configured_client_id_on_cimd_server_keeps_configured_client_id",
			fp:           discoveryFixtureParams{cimdSupported: true, clientID: "my-configured-client"},
			wantClientID: "my-configured-client",
			denyClientID: auth.ClientMetadataURL,
		},
		{
			name:         "no_cimd_advert_does_not_use_cimd_client_id",
			fp:           discoveryFixtureParams{},
			denyClientID: auth.ClientMetadataURL,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDiscoveryFixture(t, tc.fp)
			if tc.afterSetup != nil {
				tc.afterSetup(t, f)
			}
			if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer old-access"); err != nil {
				t.Fatalf("RefreshAuthorization: %v", err)
			}
			if tc.moreCalls != nil {
				tc.moreCalls(t, f)
			}
			clientID := clientIDSentToTokenEndpoint(f.endpoint)
			if tc.wantClientID != "" && clientID != tc.wantClientID {
				t.Errorf("client_id = %q, want %q", clientID, tc.wantClientID)
			}
			if tc.denyClientID != "" && clientID == tc.denyClientID {
				t.Errorf("client_id = %q, must not be %q", clientID, tc.denyClientID)
			}
		})
	}
}

func TestRefreshAuthorization_discoveryNetworkError_returnsTransientError(t *testing.T) {
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)

	endpoint := authtest.NewTokenServer(t)
	tokenPOSTs := endpoint.Hits.Load()

	discovery := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   endpoint.Srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	t.Cleanup(discovery.Close)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{
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
	if strings.Contains(err.Error(), "mini auth") {
		t.Errorf("discovery network error must not be a reauth remedy, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "(transient)") {
		t.Errorf("discovery network error must say (transient), got %q", err.Error())
	}
	if endpoint.Hits.Load() != tokenPOSTs {
		t.Errorf("token endpoint was called %d times after discovery failure, want 0", endpoint.Hits.Load()-tokenPOSTs)
	}
}

func TestRefreshAuthorization_noServerURLOrTokenURL_returnsReauthRemedyWithoutNetwork(t *testing.T) {
	endpoint := authtest.NewTokenServer(t)

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "access", RefreshToken: "refresh",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{
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
	if endpoint.Hits.Load() != 0 {
		t.Errorf("token endpoint called %d times, want 0", endpoint.Hits.Load())
	}
}

func TestRefreshAuthorization_prm503_isTransientAndSkipsMCPOrigin(t *testing.T) {
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
	p, err := provider.New(provider.Params{
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
	if errors.Is(err, transport.ErrReauthRequired) || !strings.Contains(err.Error(), "(transient)") {
		t.Errorf("PRM 503 must be a transient error, got: %v", err)
	}
}

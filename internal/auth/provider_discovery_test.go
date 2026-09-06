//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

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

	errCh := make(chan error, 1)
	go func() {
		_, err := p.Authorization(context.Background())
		errCh <- err
	}()
	advanceForBackoffs(t, clk, []time.Duration{time.Second, 2 * time.Second})
	err = <-errCh
	if err == nil {
		t.Fatal("expected error when endpoint discovery fails")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("transient discovery failure must not require reauthorization: %v", err)
	}
}

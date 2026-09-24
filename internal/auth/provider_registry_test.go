//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestProviderRegistry_closeAbortsRefresh(t *testing.T) {
	reached := make(chan struct{}, 1)
	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		select {
		case <-released:
		case <-r.Context().Done():
		}
		http.Error(w, "aborted", http.StatusInternalServerError)
	}))
	t.Cleanup(func() { release(); srv.Close() })

	dir := t.TempDir()
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: epoch.Add(-time.Second)}
	if err := auth.Save(dir, "srv", expired); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "c", TokenURL: srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clock.NewFakeAt(epoch),
	}
	registry := auth.NewProviderRegistry()
	provider, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	authDone := make(chan struct{})
	go func() {
		defer close(authDone)
		provider.Authorization(context.Background())
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}

	registry.Close()
	release() // unblock the handler so srv.Close in cleanup does not hang

	select {
	case <-authDone:
	case <-time.After(5 * time.Second):
		t.Error("registry.Close() did not abort in-flight refresh within 5s")
	}
}

func TestProviderRegistry_staleDriftDoesNotBlockRedial(t *testing.T) {
	cases := []struct {
		name    string
		initial func(t *testing.T, dir string) auth.ProviderParams
		between func(t *testing.T, dir string, reg *auth.ProviderRegistry)
		redial  func(t *testing.T, dir string) auth.ProviderParams
	}{
		{
			name: "expired_client_secret",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				if err := auth.SaveRegistration(dir, "srv", &auth.Registration{
					ClientID: "dcr-client", ClientSecret: "secret",
					TokenEndpointAuthMethod: "client_secret_basic",
					ClientSecretExpiresAt:   100,
				}); err != nil {
					t.Fatal(err)
				}
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFakeAt(time.Unix(0, 0)),
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFakeAt(time.Unix(200, 0)),
				}
			},
		},
		{
			name: "external_auth_updates_registration",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
			between: func(t *testing.T, dir string, reg *auth.ProviderRegistry) {
				if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "dcr-new"}); err != nil {
					t.Fatal(err)
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
		},
		{
			name: "stale_token_url_after_commit",
			initial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
			between: func(t *testing.T, dir string, reg *auth.ProviderRegistry) {
				committed := auth.ProviderParams{
					AuthConfig: &config.AuthConfig{
						Type: config.AuthTypeOAuth2, ClientID: "discovered",
						AuthURL: "https://as.example.com/auth", TokenURL: "https://as.example.com/token",
					},
					ConfigDir: dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
				tok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
				if err := reg.CommitAuthorizedToken(committed, tok); err != nil {
					t.Fatalf("CommitAuthorizedToken: %v", err)
				}
			},
			redial: func(t *testing.T, dir string) auth.ProviderParams {
				return auth.ProviderParams{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
					ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com",
					Clock: clock.NewFake(),
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			reg := auth.NewProviderRegistry()
			first, err := reg.GetOrCreate(tc.initial(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if tc.between != nil {
				tc.between(t, dir, reg)
			}
			second, err := reg.GetOrCreate(tc.redial(t, dir))
			if err != nil {
				t.Fatalf("re-dial rejected: %v", err)
			}
			if first != second {
				t.Fatal("re-dial returned a different provider")
			}
		})
	}
}

func TestProviderRegistry_reusesProviderPerServer(t *testing.T) {
	registry := auth.NewProviderRegistry()
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		Clock:      clock.NewFake(),
	}
	first, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("registry returned different providers for the same server")
	}
}

func TestProviderRegistry_commitReusesProviderAndInstallsToken(t *testing.T) {
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	registry := auth.NewProviderRegistry()
	before, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	authorized := params
	authorized.AuthConfig = &config.AuthConfig{
		Type:     config.AuthTypeOAuth2,
		ClientID: "discovered-client",
		AuthURL:  "https://auth.example.com/authorize",
		TokenURL: "https://auth.example.com/token",
	}
	token := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := registry.CommitAuthorizedToken(authorized, token); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	after, err := registry.GetOrCreate(authorized)
	if err != nil {
		t.Fatalf("GetOrCreate after commit: %v", err)
	}
	if before != after {
		t.Fatal("authorized token commit replaced the provider")
	}
	got, err := after.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
}

func TestProviderRegistry_incompatibleParamsRejectedWithoutMutation(t *testing.T) {
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	registry := auth.NewProviderRegistry()
	original, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	changed := params
	changed.ServerURL = "https://other.example.com/mcp"
	if _, err := registry.GetOrCreate(changed); err == nil {
		t.Fatal("incompatible provider identity was accepted")
	}
	again, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if original != again {
		t.Fatal("rejected configuration mutated the registered provider")
	}
}

func TestProviderRegistry_browserCommitDuringRefreshWins(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	endpointHit := make(chan struct{})
	releaseEndpoint := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(endpointHit)
		<-releaseEndpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "refresh-result", "refresh_token": "refresh-rotated",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenServer.Close)
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: tokenServer.URL,
			TokenEndpointAuthMethod: "client_secret_post",
		},
		ConfigDir: dir, ServerName: "srv", ServerURL: "https://mcp.example.com/mcp", Clock: clk,
	}
	registry := auth.NewProviderRegistry()
	provider, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		refreshDone <- err
	}()
	<-endpointHit
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- registry.CommitAuthorizedToken(params, &oauth2.Token{
			AccessToken: "browser-access", RefreshToken: "browser-refresh",
		})
	}()
	close(releaseEndpoint)
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := <-commitDone; err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := provider.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "browser-access" {
		t.Fatalf("saved access token = %q, want browser token", saved.AccessToken)
	}
}

func TestProviderRegistry_commitHydratesRegistration(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()

	reg1 := &auth.Registration{ClientID: "dcr-v1", ClientSecret: "secret-v1", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg1); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	registry := auth.NewProviderRegistry()
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	endpoint.mu.Lock()
	gotV1 := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	if gotV1 != "dcr-v1" {
		t.Errorf("initial basic auth user = %q, want dcr-v1", gotV1)
	}

	reg2 := &auth.Registration{ClientID: "dcr-v2", ClientSecret: "secret-v2", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg2); err != nil {
		t.Fatal(err)
	}
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	endpoint.mu.Lock()
	endpoint.accessToken, endpoint.refreshToken = "new-access2", "new-refresh2"
	endpoint.mu.Unlock()
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("post-commit refresh: %v", err)
	}
	endpoint.mu.Lock()
	gotV2 := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	if gotV2 != "dcr-v2" {
		t.Errorf("post-commit basic auth user = %q, want dcr-v2 (commit must hydrate new registration)", gotV2)
	}
}

func TestProviderRegistry_commitSaveFailureLeavesProviderUnchanged(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	registry := auth.NewProviderRegistry()
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := registry.CommitAuthorizedToken(params, browserTok); err == nil {
		t.Fatal("expected CommitAuthorizedToken to fail when persist is denied")
	}
	os.Chmod(internal, 0700) //nolint:errcheck

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after failed commit: %v", err)
	}
	if got == "Bearer browser-access" {
		t.Error("failed commit must not update in-memory token")
	}
}

func TestProviderRegistry_commitServesTokenFromMemory(t *testing.T) {
	dir := t.TempDir()
	endpoint := newTokenEndpoint(t)
	clk := clock.NewFake()
	initial := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r", Expiry: clk.Now().Add(time.Hour)}
	if err := auth.Save(dir, "srv", initial); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	registry := auth.NewProviderRegistry()
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("initial Authorization: %v", err)
	}

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-r", Expiry: clk.Now().Add(time.Hour)}
	if err := registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}

	// Remove token file: provider must serve browser token from in-memory state only.
	os.RemoveAll(dir + "/internal")

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after commit (no disk): %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("Authorization = %q, want Bearer browser-access", got)
	}
	if endpoint.hits.Load() != 0 {
		t.Errorf("token endpoint hit = %d, want 0", endpoint.hits.Load())
	}
}

func TestProviderRegistry_commitForDifferentServerURLRejected(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	stored := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r", Expiry: clk.Now().Add(time.Hour)}
	if err := auth.Save(dir, "srv", stored); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  dir, ServerName: "srv", ServerURL: "https://a.example.com/mcp", Clock: clk,
	}
	registry := auth.NewProviderRegistry()
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	moved := params
	moved.ServerURL = "https://b.example.com/mcp"
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-r", Expiry: clk.Now().Add(time.Hour)}
	if err := registry.CommitAuthorizedToken(moved, browserTok); err == nil {
		t.Fatal("commit for a different server URL must be rejected")
	}

	if got, _ := p.Authorization(context.Background()); got != "Bearer stored-access" {
		t.Errorf("provider serves %q, want the original token", got)
	}
	onDisk, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.AccessToken != "stored-access" {
		t.Errorf("disk token = %q, want the original token", onDisk.AccessToken)
	}
}

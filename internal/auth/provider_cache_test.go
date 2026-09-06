//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

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

func TestProviderCache_equivalentGetOrCreatePreservesIdentity(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
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
	p2, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Error("equivalent GetOrCreate must return the same provider pointer")
	}
}

func TestProviderCache_commitAuthorizedTokenPreservesIdentity(t *testing.T) {
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
	cache := auth.NewProviderCache()
	p1, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	p2, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Error("CommitAuthorizedToken must not replace the cached provider pointer")
	}
}

func TestProviderCache_commitUpdatesEffectiveIdentity(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	initial := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()
	p1, err := cache.GetOrCreate(initial)
	if err != nil {
		t.Fatal(err)
	}
	authorized := initial
	authorized.AuthConfig = &config.AuthConfig{
		Type:     config.AuthTypeOAuth2,
		ClientID: "discovered-client",
		AuthURL:  "https://auth.example.com/authorize",
		TokenURL: "https://auth.example.com/token",
	}
	if err := cache.CommitAuthorizedToken(authorized, &oauth2.Token{AccessToken: "browser-access"}); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	p2, err := cache.GetOrCreate(authorized)
	if err != nil {
		t.Fatalf("GetOrCreate after trusted commit: %v", err)
	}
	if p1 != p2 {
		t.Error("trusted commit must retain the provider pointer while replacing its effective identity")
	}
}

func TestProviderCache_commitWaitsForRefreshAndNewTokenWins(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}

	endpointHit := make(chan struct{})
	releaseEndpoint := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(endpointHit)
		<-releaseEndpoint
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "refresh-result", "refresh_token": "refresh-rt",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid",
			TokenEndpointAuthMethod: "client_secret_post", TokenURL: srv.URL},
		ConfigDir:  dir,
		ServerName: "srv",
		Clock:      clk,
	}
	cache := auth.NewProviderCache()
	p, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access")
		refreshDone <- err
	}()

	<-endpointHit

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- cache.CommitAuthorizedToken(params, browserTok)
	}()

	// Commit cannot proceed until refresh releases p.mu; the non-blocking check
	// proves commit is waiting. Gosched yields to give the commit goroutine a chance
	// to reach the lock before we proceed.
	runtime.Gosched()
	select {
	case err := <-commitDone:
		t.Fatalf("CommitAuthorizedToken must not return while refresh holds the lock: %v", err)
	default:
	}

	close(releaseEndpoint)
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := <-commitDone; err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after commit: %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("got %q, want Bearer browser-access (browser token must win over refresh result)", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.AccessToken != "browser-access" {
		t.Errorf("persisted token = %q, want browser-access", saved.AccessToken)
	}
}

func TestProviderCache_commitHydratesRegistration(t *testing.T) {
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
	cache := auth.NewProviderCache()
	p, err := cache.GetOrCreate(params)
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
	if err := cache.CommitAuthorizedToken(params, browserTok); err != nil {
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

func TestProviderCache_commitSaveFailureLeavesProviderUnchanged(t *testing.T) {
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
	cache := auth.NewProviderCache()
	p, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err == nil {
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

func TestProviderCache_incompatibleParamsRejectedWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
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
	incompatible := params
	incompatible.ServerURL = "https://different.example.com/mcp"
	_, err = cache.GetOrCreate(incompatible)
	if err == nil {
		t.Fatal("incompatible params must return an error")
	}
	p2, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatalf("original params must still work after incompatible attempt: %v", err)
	}
	if p1 != p2 {
		t.Error("incompatible attempt must not mutate the cached provider")
	}
}

func TestOAuthReconnect_perSessionProviderSeesAuthorizedToken(t *testing.T) {
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
	cache := auth.NewProviderCache()
	primary, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	perSession, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if primary != perSession {
		t.Fatal("GetOrCreate must return the same provider for equivalent params")
	}
	browserTok := &oauth2.Token{AccessToken: "authorized-access", RefreshToken: "authorized-refresh"}
	if err := cache.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	for name, p := range map[string]transport.AuthorizationProvider{"primary": primary, "perSession": perSession} {
		got, err := p.Authorization(context.Background())
		if err != nil {
			t.Fatalf("%s Authorization: %v", name, err)
		}
		if got != "Bearer authorized-access" {
			t.Errorf("%s got %q, want Bearer authorized-access", name, got)
		}
	}
}

func TestRemoveServer_readdDoesNotCreateSecondProvider(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
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
	// Simulate remove_server: provider is NOT evicted (the fix — Evict is no longer called)
	p2, err := cache.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Error("re-add must return the same provider; a second provider would race concurrent refreshes")
	}
}

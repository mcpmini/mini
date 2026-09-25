//go:build test

package auth_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type commitFixture struct {
	dir      string
	clock    *clock.Fake
	registry *auth.ProviderRegistry
	endpoint *mockAuthServer
}

func newCommitFixture(t *testing.T) *commitFixture {
	t.Helper()
	return &commitFixture{
		dir:      t.TempDir(),
		clock:    clock.NewFake(),
		registry: auth.NewProviderRegistry(),
		endpoint: newMockAuthServer(t),
	}
}

func (f *commitFixture) params() auth.ProviderParams {
	return f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"})
}

func (f *commitFixture) paramsFor(ac *config.AuthConfig) auth.ProviderParams {
	cfg := *ac
	if cfg.TokenURL == "" {
		cfg.TokenURL = f.endpoint.srv.URL + "/token"
	}
	return auth.ProviderParams{
		AuthConfig: &cfg,
		ConfigDir:  f.dir,
		ServerName: "srv",
		Clock:      f.clock,
	}
}

func TestCommitAuthorizedToken_existingProvider_servesFromMemoryAfterFileRemoved(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()

	before, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	browserTok := &oauth2.Token{
		AccessToken: "browser-access", RefreshToken: "browser-r",
		Expiry: f.clock.Now().Add(time.Hour),
	}
	if err := f.registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	after, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatalf("GetOrCreate after commit: %v", err)
	}
	if before != after {
		t.Fatal("commit replaced the provider")
	}

	os.RemoveAll(f.dir + "/internal") //nolint:errcheck

	got, err := after.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after file removed: %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("Authorization = %q, want Bearer browser-access", got)
	}
}

func TestCommitAuthorizedToken_duringRefresh_browserTokenWins(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()

	if err := auth.Save(f.dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	f.endpoint.accessToken = "refresh-result"
	rawReceived, rawRelease := gateNextTokenRequest(f.endpoint)
	var once sync.Once
	release := func() { once.Do(rawRelease) }
	t.Cleanup(release)

	provider, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		refreshDone <- err
	}()
	select {
	case <-rawReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- f.registry.CommitAuthorizedToken(params, &oauth2.Token{
			AccessToken: "browser-access", RefreshToken: "browser-refresh",
		})
	}()
	release()
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
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "browser-access" {
		t.Fatalf("saved token = %q, want browser-access", saved.AccessToken)
	}
}

func TestCommitAuthorizedToken_withStoredRegistration_usesItsClientCredentials(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})

	reg1 := &auth.Registration{ClientID: "dcr-v1", ClientSecret: "secret-v1", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(f.dir, "srv", reg1); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(f.dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	f.endpoint.mu.Lock()
	gotV1 := f.endpoint.lastBasicAuth
	f.endpoint.mu.Unlock()
	if gotV1 != "dcr-v1" {
		t.Errorf("initial basic auth user = %q, want dcr-v1", gotV1)
	}

	reg2 := &auth.Registration{ClientID: "dcr-v2", ClientSecret: "secret-v2", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(f.dir, "srv", reg2); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.CommitAuthorizedToken(params, &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("post-commit refresh: %v", err)
	}
	f.endpoint.mu.Lock()
	gotV2 := f.endpoint.lastBasicAuth
	f.endpoint.mu.Unlock()
	if gotV2 != "dcr-v2" {
		t.Errorf("post-commit basic auth user = %q, want dcr-v2 (commit must hydrate new registration)", gotV2)
	}
}

func TestCommitAuthorizedToken_saveFails_providerUnchanged(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	if err := auth.Save(f.dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := f.dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	if err := f.registry.CommitAuthorizedToken(params, &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}); err == nil {
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

func TestCommitAuthorizedToken_differentServerURL_rejected(t *testing.T) {
	f := newCommitFixture(t)
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"},
		ConfigDir:  f.dir, ServerName: "srv", ServerURL: "https://a.example.com/mcp", Clock: f.clock,
	}
	stored := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r", Expiry: f.clock.Now().Add(time.Hour)}
	if err := auth.Save(f.dir, "srv", stored); err != nil {
		t.Fatal(err)
	}
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	moved := params
	moved.ServerURL = "https://b.example.com/mcp"
	if err := f.registry.CommitAuthorizedToken(moved, &oauth2.Token{AccessToken: "browser-access"}); err == nil {
		t.Fatal("commit for different server URL must be rejected")
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer stored-access" {
		t.Errorf("provider serves %q, want the original token", got)
	}
	onDisk, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.AccessToken != "stored-access" {
		t.Errorf("disk token = %q, want the original token", onDisk.AccessToken)
	}
}

func TestCommitAuthorizedToken_noProviderYet_savesTokenForLaterDial(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := f.registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken without provider: %v", err)
	}
	provider, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	got, err := provider.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("Authorization = %q, want Bearer browser-access", got)
	}
}

func TestCommitAuthorizedToken_externalReregistration_usesNewRegistrationCredentials(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})

	reg1 := &auth.Registration{ClientID: "dcr-v1", ClientSecret: "secret-v1", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(f.dir, "srv", reg1); err != nil {
		t.Fatal(err)
	}
	initialTok := &oauth2.Token{AccessToken: "initial-access", RefreshToken: "initial-refresh", Expiry: f.clock.Now().Add(time.Hour)}
	if err := auth.Save(f.dir, "srv", initialTok); err != nil {
		t.Fatal(err)
	}

	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh", Expiry: f.clock.Now().Add(time.Hour)}
	if err := f.registry.CommitAuthorizedToken(params, browserTok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}

	reg2 := &auth.Registration{ClientID: "dcr-v2", ClientSecret: "secret-v2", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(f.dir, "srv", reg2); err != nil {
		t.Fatal(err)
	}
	externalTok := &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh", Expiry: f.clock.Now().Add(time.Hour)}
	if err := auth.Save(f.dir, "srv", externalTok); err != nil {
		t.Fatal(err)
	}

	got, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization for adoption: %v", err)
	}
	if got != "Bearer external-access" {
		t.Fatalf("expected adoption of external token, got %q", got)
	}

	f.clock.Advance(2 * time.Hour)
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization after expiry: %v", err)
	}

	f.endpoint.mu.Lock()
	clientIDForm, clientIDBasic := f.endpoint.lastClientID, f.endpoint.lastBasicAuth
	f.endpoint.mu.Unlock()
	if clientIDForm != "dcr-v2" && clientIDBasic != "dcr-v2" {
		t.Errorf("client_id = form:%q basic:%q, want dcr-v2 (preHydrationAuthConfig must not carry a resolved ClientID)", clientIDForm, clientIDBasic)
	}
}

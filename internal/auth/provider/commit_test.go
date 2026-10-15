//go:build test

package provider_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type commitFixture struct {
	dir      string
	clock    *clock.Fake
	registry *provider.Registry
	endpoint *authtest.TokenServer
}

func newCommitFixture(t *testing.T) *commitFixture {
	t.Helper()
	return &commitFixture{
		dir:      t.TempDir(),
		clock:    clock.NewFake(),
		registry: provider.NewRegistry(),
		endpoint: authtest.NewTokenServer(t),
	}
}

func (f *commitFixture) params() provider.Params {
	return f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"})
}

func (f *commitFixture) paramsFor(ac *config.AuthConfig) provider.Params {
	cfg := *ac
	if cfg.TokenURL == "" {
		cfg.TokenURL = f.endpoint.Srv.URL + "/token"
	}
	return provider.Params{
		AuthConfig: &cfg,
		ConfigDir:  f.dir,
		ServerName: "srv",
		ServerURL:  testServerURL,
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
	if commitErr := f.registry.CommitAuthorizedToken(params, loginFor(t, params, browserTok)); commitErr != nil {
		t.Fatalf("CommitAuthorizedToken: %v", commitErr)
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

	saveLoginFor(t, params, storedToken(time.Time{}))
	f.endpoint.AccessToken = "refresh-result"
	rawReceived, rawRelease := gateNextTokenRequest(f.endpoint)
	var once sync.Once
	release := func() { once.Do(rawRelease) }
	t.Cleanup(release)

	prov, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := prov.RefreshAuthorization(context.Background(), "Bearer stored-access")
		refreshDone <- refreshErr
	}()
	select {
	case <-rawReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}
	commitDone := make(chan error, 1)
	go func() {
		browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
		commitDone <- f.registry.CommitAuthorizedToken(params, loginFor(t, params, browserTok))
	}()
	release()
	if refreshErr := <-refreshDone; refreshErr != nil {
		t.Fatalf("refresh: %v", refreshErr)
	}
	if commitErr := <-commitDone; commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	got, err := prov.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
	saved, err := loadSavedToken(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "browser-access" {
		t.Fatalf("saved token = %q, want browser-access", saved.AccessToken)
	}
}

func TestCommitAuthorizedToken_refreshUsesTheClientEachLoginWasIssuedTo(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{ConfigDir: f.dir, ServerName: "srv", Registration: &auth.Registration{
			ClientID: "dcr-unrelated", ClientSecret: "secret-unrelated", TokenEndpointAuthMethod: "client_secret_basic",
		}},
	)
	first := registeredLogin(t, params, storedToken(time.Time{}), "dcr-v1")
	if err := auth.SaveLogin(f.dir, "srv", first); err != nil {
		t.Fatal(err)
	}
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	assertRefreshedAs(t, f.endpoint, "dcr-v1")

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := f.registry.CommitAuthorizedToken(params, registeredLogin(t, params, browserTok, "dcr-v2")); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("post-commit refresh: %v", err)
	}
	assertRefreshedAs(t, f.endpoint, "dcr-v2")
}

func registeredLogin(t *testing.T, params provider.Params, token *oauth2.Token, clientID string) *auth.Login {
	t.Helper()
	login := loginFor(t, params, token)
	login.Client = auth.LoginClient{ID: clientID, Secret: "secret-" + clientID, AuthMethod: "client_secret_basic"}
	return login
}

func assertRefreshedAs(t *testing.T, endpoint *authtest.TokenServer, clientID string) {
	t.Helper()
	endpoint.Mu.Lock()
	got := endpoint.LastBasicAuth
	endpoint.Mu.Unlock()
	if got != clientID {
		t.Errorf("refresh authenticated as %q, want %q", got, clientID)
	}
}

func TestCommitAuthorizedToken_saveFails_providerUnchanged(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	saveLoginFor(t, params, storedToken(time.Time{}))
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := f.dir + "/internal"
	if chmodErr := os.Chmod(internal, 0o500); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	t.Cleanup(func() { os.Chmod(internal, 0o700) }) //nolint:errcheck

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if commitErr := f.registry.CommitAuthorizedToken(params, loginFor(t, params, browserTok)); commitErr == nil {
		t.Fatal("expected CommitAuthorizedToken to fail when persist is denied")
	}
	os.Chmod(internal, 0o700) //nolint:errcheck

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
	params := provider.Params{
		AuthConfig: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "cid",
			TokenURL: "http://localhost:1/token",
		},
		ConfigDir:  f.dir,
		ServerName: "srv",
		ServerURL:  "https://a.example.com/mcp",
		Clock:      f.clock,
	}
	stored := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r", Expiry: f.clock.Now().Add(time.Hour)}
	saveLoginFor(t, params, stored)
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	moved := params
	moved.ServerURL = "https://b.example.com/mcp"
	browserTok := &oauth2.Token{AccessToken: "browser-access"}
	if commitErr := f.registry.CommitAuthorizedToken(moved, loginFor(t, moved, browserTok)); commitErr == nil {
		t.Fatal("commit for different server URL must be rejected")
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer stored-access" {
		t.Errorf("provider serves %q, want the original token", got)
	}
	onDisk, err := loadSavedToken(f.dir)
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
	if err := f.registry.CommitAuthorizedToken(params, loginFor(t, params, browserTok)); err != nil {
		t.Fatalf("CommitAuthorizedToken without provider: %v", err)
	}
	prov, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	got, err := prov.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("Authorization = %q, want Bearer browser-access", got)
	}
}

func TestCommitAuthorizedToken_loginAfterEditingClientID_isSavedAndUsed(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	saveLoginFor(t, params, storedToken(time.Time{}))
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	edited := params
	edited.AuthConfig = params.AuthConfig.Clone()
	edited.AuthConfig.ClientID = "edited-client"
	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}

	if err := f.registry.CommitAuthorizedToken(edited, loginFor(t, edited, browserTok)); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	if saved, err := loadSavedToken(f.dir); err != nil || saved.AccessToken != "browser-access" {
		t.Fatalf("saved token = %v, %v; want the new login saved", saved, err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	assertRefreshedAs(t, f.endpoint, "edited-client")
}

func TestRefreshAuthorization_adoptsAnotherProcesssLoginWithItsClient(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})
	saveRegisteredLogin(t, params, &oauth2.Token{AccessToken: "first-access", RefreshToken: "first-refresh"}, "dcr-v1")
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, authErr := p.Authorization(context.Background()); authErr != nil {
		t.Fatal(authErr)
	}
	external := &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}
	saveRegisteredLogin(t, params, external, "dcr-v2")

	got, err := p.RefreshAuthorization(context.Background(), "Bearer first-access")
	if err != nil || got != "Bearer external-access" {
		t.Fatalf("RefreshAuthorization = %q, %v; want the other process's login", got, err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer external-access"); err != nil {
		t.Fatalf("refresh of the adopted login: %v", err)
	}
	assertRefreshedAs(t, f.endpoint, "dcr-v2")
}

func TestRefreshAuthorization_loginSavedForOtherSettings_isNeitherUsedNorOverwritten(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	saveLoginFor(t, params, storedToken(time.Time{}))
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, authErr := p.Authorization(context.Background()); authErr != nil {
		t.Fatal(authErr)
	}
	edited := params
	edited.ServerURL = "https://moved.example.com/mcp"
	saveLoginFor(t, edited, &oauth2.Token{AccessToken: "login-for-new-url", RefreshToken: "new-refresh"})

	_, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil || !strings.Contains(err.Error(), "mini auth") {
		t.Fatalf("RefreshAuthorization = %v, want the login remedy", err)
	}
	if hits := f.endpoint.Hits.Load(); hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0", hits)
	}
	if saved, _ := loadSavedToken(f.dir); saved == nil || saved.AccessToken != "login-for-new-url" {
		t.Errorf("saved login = %v, want the new URL's login left in place", saved)
	}
}

func saveRegisteredLogin(t *testing.T, params provider.Params, token *oauth2.Token, clientID string) {
	t.Helper()
	if err := auth.SaveLogin(
		params.ConfigDir,
		params.ServerName,
		registeredLogin(t, params, token, clientID),
	); err != nil {
		t.Fatal(err)
	}
}

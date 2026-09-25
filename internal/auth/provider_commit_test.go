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

func TestCommitAuthorizedToken_existingProvider_servesBrowserTokenImmediately(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com/mcp",
		Clock: clk,
	}
	registry := auth.NewProviderRegistry()
	before, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	tok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	authorized := params
	authorized.AuthConfig = &config.AuthConfig{
		Type: config.AuthTypeOAuth2, ClientID: "discovered",
		AuthURL: "https://as.example.com/auth", TokenURL: "https://as.example.com/token",
	}
	if err := registry.CommitAuthorizedToken(authorized, tok); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	after, err := registry.GetOrCreate(authorized)
	if err != nil {
		t.Fatalf("GetOrCreate after commit: %v", err)
	}
	if before != after {
		t.Fatal("commit replaced the provider")
	}
	got, err := after.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer browser-access" {
		t.Fatalf("Authorization = %q, want browser token", got)
	}
}

func TestCommitAuthorizedToken_duringRefresh_browserTokenWins(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	endpoint := newMockAuthServer(t)
	endpoint.accessToken = "refresh-result"
	rawReceived, rawRelease := gateNextTokenRequest(endpoint)
	var once sync.Once
	release := func() { once.Do(rawRelease) }
	t.Cleanup(release)

	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid",
			TokenURL: endpoint.srv.URL + "/token",
		},
		ConfigDir: dir, ServerName: "srv", Clock: clk,
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
	select {
	case <-rawReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- registry.CommitAuthorizedToken(params, &oauth2.Token{
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
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "browser-access" {
		t.Fatalf("saved token = %q, want browser-access", saved.AccessToken)
	}
}

func TestCommitAuthorizedToken_withStoredRegistration_usesItsClientCredentials(t *testing.T) {
	dir := t.TempDir()
	endpoint := newMockAuthServer(t)
	clk := clock.NewFake()

	reg1 := &auth.Registration{ClientID: "dcr-v1", ClientSecret: "secret-v1", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg1); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
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

func TestCommitAuthorizedToken_saveFails_providerUnchanged(t *testing.T) {
	dir := t.TempDir()
	endpoint := newMockAuthServer(t)
	clk := clock.NewFake()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
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

func TestCommitAuthorizedToken_tokenFileRemoved_stillServesFromMemory(t *testing.T) {
	dir := t.TempDir()
	endpoint := newMockAuthServer(t)
	clk := clock.NewFake()
	initial := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r", Expiry: clk.Now().Add(time.Hour)}
	if err := auth.Save(dir, "srv", initial); err != nil {
		t.Fatal(err)
	}
	params := auth.ProviderParams{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
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
	os.RemoveAll(dir + "/internal") //nolint:errcheck

	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after commit (no disk): %v", err)
	}
	if got != "Bearer browser-access" {
		t.Errorf("Authorization = %q, want Bearer browser-access", got)
	}
	if endpoint.hits.Load() != 0 {
		t.Errorf("token endpoint hits = %d, want 0", endpoint.hits.Load())
	}
}

func TestCommitAuthorizedToken_differentServerURL_rejected(t *testing.T) {
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
		t.Fatal("commit for different server URL must be rejected")
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer stored-access" {
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

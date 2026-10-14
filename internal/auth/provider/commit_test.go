//go:build test

package provider_test

import (
	"context"
	"os"
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
		ServerURL:  "https://resource.example.com/mcp",
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
	if commitErr := f.registry.CommitAuthorizedToken(
		params,
		issuingLogin(params, auth.Client{Source: auth.ClientConfigured, ID: params.AuthConfig.ClientID}, browserTok),
	); commitErr != nil {
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

	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   f.endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientConfigured, ID: "cid"},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      storedToken(time.Time{}),
		},
	)
	f.endpoint.AccessToken = "refresh-result"
	rawReceived, release := gateNextTokenRequest(t, f.endpoint)

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
		commitDone <- f.registry.CommitAuthorizedToken(params, issuingLogin(params, auth.Client{Source: auth.ClientConfigured, ID: params.AuthConfig.ClientID}, &oauth2.Token{
			AccessToken: "browser-access", RefreshToken: "browser-refresh",
		}))
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
	saved, err := auth.LoadLogin(f.dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.AccessToken != "browser-access" {
		t.Fatalf("saved token = %q, want browser-access", saved.Token.AccessToken)
	}
}

func TestCommitAuthorizedToken_loginOwnsTheIssuingRegisteredClient(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})

	reg1 := &auth.Registration{
		ClientID:                "dcr-v1",
		ClientSecret:            "secret-v1",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	authtest.SaveRegistration(t, authtest.RegistrationFile{ConfigDir: f.dir, ServerName: "srv", Registration: reg1})
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource: "https://resource.example.com/mcp",
			TokenURL: f.endpoint.Srv.URL + "/token",
			Client: auth.Client{
				Source:     auth.ClientRegistered,
				ID:         "dcr-v1",
				Secret:     "secret-v1",
				AuthMethod: "client_secret_basic",
			},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      storedToken(time.Time{}),
		},
	)
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	f.endpoint.Mu.Lock()
	gotV1 := f.endpoint.LastBasicAuth
	f.endpoint.Mu.Unlock()
	if gotV1 != "dcr-v1" {
		t.Errorf("initial basic auth user = %q, want dcr-v1", gotV1)
	}

	reg2 := &auth.Registration{
		ClientID:                "dcr-v2",
		ClientSecret:            "secret-v2",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	authtest.SaveRegistration(t, authtest.RegistrationFile{ConfigDir: f.dir, ServerName: "srv", Registration: reg2})
	if err := f.registry.CommitAuthorizedToken(
		params,
		issuingLogin(
			params,
			auth.Client{
				Source:     auth.ClientRegistered,
				ID:         "dcr-v2",
				Secret:     "secret-v2",
				AuthMethod: "client_secret_basic",
			},
			&oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"},
		),
	); err != nil {
		t.Fatalf("CommitAuthorizedToken: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer browser-access"); err != nil {
		t.Fatalf("post-commit refresh: %v", err)
	}
	f.endpoint.Mu.Lock()
	gotV2 := f.endpoint.LastBasicAuth
	f.endpoint.Mu.Unlock()
	if gotV2 != "dcr-v2" {
		t.Errorf("post-commit basic auth user = %q, want the client recorded in the committed login", gotV2)
	}
}

func TestCommitAuthorizedToken_saveFails_providerUnchanged(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource:   "https://resource.example.com/mcp",
			TokenURL:   f.endpoint.Srv.URL + "/token",
			Client:     auth.Client{Source: auth.ClientConfigured, ID: "cid"},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      storedToken(time.Time{}),
		},
	)
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	internal := f.dir + "/internal"
	if chmodErr := os.Chmod(internal, 0o500); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	t.Cleanup(func() { os.Chmod(internal, 0o700) }) //nolint:errcheck

	if commitErr := f.registry.CommitAuthorizedToken(
		params,
		issuingLogin(
			params,
			auth.Client{Source: auth.ClientConfigured, ID: params.AuthConfig.ClientID},
			&oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"},
		),
	); commitErr == nil {
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
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource:   params.ServerURL,
			TokenURL:   params.AuthConfig.TokenURL,
			Client:     auth.Client{Source: auth.ClientConfigured, ID: "cid"},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      stored,
		},
	)
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	moved := params
	moved.ServerURL = "https://b.example.com/mcp"
	if commitErr := f.registry.CommitAuthorizedToken(
		moved,
		issuingLogin(
			moved,
			auth.Client{Source: auth.ClientConfigured, ID: moved.AuthConfig.ClientID},
			&oauth2.Token{AccessToken: "browser-access"},
		),
	); commitErr == nil {
		t.Fatal("commit for different server URL must be rejected")
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer stored-access" {
		t.Errorf("provider serves %q, want the original token", got)
	}
	onDisk, err := auth.LoadLogin(f.dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Token.AccessToken != "stored-access" {
		t.Errorf("disk token = %q, want the original token", onDisk.Token.AccessToken)
	}
}

func TestCommitAuthorizedToken_noProviderYet_savesTokenForLaterDial(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()

	browserTok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if err := f.registry.CommitAuthorizedToken(
		params,
		issuingLogin(params, auth.Client{Source: auth.ClientConfigured, ID: params.AuthConfig.ClientID}, browserTok),
	); err != nil {
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

func TestCommitAuthorizedToken_externalLoginCarriesItsOwnClient(t *testing.T) {
	f := newCommitFixture(t)
	params := f.paramsFor(&config.AuthConfig{Type: config.AuthTypeOAuth2})

	reg1 := &auth.Registration{
		ClientID:                "dcr-v1",
		ClientSecret:            "secret-v1",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	authtest.SaveRegistration(t, authtest.RegistrationFile{ConfigDir: f.dir, ServerName: "srv", Registration: reg1})
	initialTok := &oauth2.Token{
		AccessToken:  "initial-access",
		RefreshToken: "initial-refresh",
		Expiry:       f.clock.Now().Add(time.Hour),
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource: "https://resource.example.com/mcp",
			TokenURL: f.endpoint.Srv.URL + "/token",
			Client: auth.Client{
				Source:     auth.ClientRegistered,
				ID:         "dcr-v1",
				Secret:     "secret-v1",
				AuthMethod: "client_secret_basic",
			},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      initialTok,
		},
	)

	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	browserTok := &oauth2.Token{
		AccessToken:  "browser-access",
		RefreshToken: "browser-refresh",
		Expiry:       f.clock.Now().Add(time.Hour),
	}
	if commitErr := f.registry.CommitAuthorizedToken(
		params,
		issuingLogin(
			params,
			auth.Client{
				Source:     auth.ClientRegistered,
				ID:         "dcr-v1",
				Secret:     "secret-v1",
				AuthMethod: "client_secret_basic",
			},
			browserTok,
		),
	); commitErr != nil {
		t.Fatalf("CommitAuthorizedToken: %v", commitErr)
	}

	reg2 := &auth.Registration{
		ClientID:                "dcr-v2",
		ClientSecret:            "secret-v2",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	authtest.SaveRegistration(t, authtest.RegistrationFile{ConfigDir: f.dir, ServerName: "srv", Registration: reg2})
	externalTok := &oauth2.Token{
		AccessToken:  "external-access",
		RefreshToken: "external-refresh",
		Expiry:       f.clock.Now().Add(time.Hour),
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			Resource: "https://resource.example.com/mcp",
			TokenURL: f.endpoint.Srv.URL + "/token",
			Client: auth.Client{
				Source:     auth.ClientRegistered,
				ID:         "dcr-v2",
				Secret:     "secret-v2",
				AuthMethod: "client_secret_basic",
			},
			ConfigDir:  f.dir,
			ServerName: "srv",
			Token:      externalTok,
		},
	)

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

	f.endpoint.Mu.Lock()
	clientIDForm, clientIDBasic := f.endpoint.LastClientID, f.endpoint.LastBasicAuth
	f.endpoint.Mu.Unlock()
	if clientIDForm != "dcr-v2" && clientIDBasic != "dcr-v2" {
		t.Errorf(
			"client_id = form:%q basic:%q, want issuing client dcr-v2 recorded by the external login",
			clientIDForm,
			clientIDBasic,
		)
	}
}

func issuingLogin(params provider.Params, client auth.Client, token *oauth2.Token) *auth.Login {
	resource, err := auth.CanonicalResourceURI(params.ServerURL)
	if err != nil {
		panic(err)
	}
	return &auth.Login{Resource: resource, TokenURL: params.AuthConfig.TokenURL, Client: client, Token: token}
}

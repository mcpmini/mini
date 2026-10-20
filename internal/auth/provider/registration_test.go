//go:build test

package provider_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
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

func TestAuthorization_loginForOtherSettings_isNeverSent(t *testing.T) {
	cases := []struct {
		name string
		edit func(*provider.Params)
		want string
	}{
		{
			"server URL",
			func(p *provider.Params) { p.ServerURL = "https://moved.example.com/mcp" },
			"server URL changed",
		},
		{"client_id", func(p *provider.Params) { p.AuthConfig.ClientID = "other-client" }, "client_id changed"},
		{"client_id removed", func(p *provider.Params) { p.AuthConfig.ClientID = "" }, "client_id changed"},
		{
			"token_url",
			func(p *provider.Params) { p.AuthConfig.TokenURL = "https://other.example/token" },
			"token_url changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := authtest.NewTokenServer(t)
			params := directParams(t.TempDir(), endpoint)
			saveLoginFor(t, params, storedToken(time.Time{}))
			edited := params
			edited.AuthConfig = params.AuthConfig.Clone()
			tc.edit(&edited)
			p, err := provider.New(edited)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Authorization(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Authorization = %q, %v; want an error saying the %s", got, err, tc.want)
			}
		})
	}
}

func TestRefreshAuthorization_registeredClient_refreshesAtItsEndpointWithItsSecret(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{ConfigDir: dir, ServerName: "srv", Registration: &auth.Registration{
			ClientID: "newer-dcr", ClientSecret: "newer-secret", TokenEndpointAuthMethod: "client_secret_basic",
		}},
	)
	params := provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv", ServerURL: testServerURL, Clock: clock.NewFake(),
	}
	login := registeredLogin(t, registeredLoginParams{
		Params:   params,
		Token:    storedToken(time.Time{}),
		ClientID: "login-client",
	})
	login.TokenURL = endpoint.Srv.URL + "/token"
	if err := auth.SaveLogin(dir, "srv", login); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.Mu.Lock()
	user, pass := endpoint.LastBasicAuth, endpoint.LastBasicPass
	endpoint.Mu.Unlock()
	if user != "login-client" || pass != "secret-login-client" {
		t.Errorf("refresh authenticated as %q:%q, want the login's client login-client:secret-login-client", user, pass)
	}
}

func TestRefreshAuthorization_configuredClient_secretComesFromConfigAndIsNeverSaved(t *testing.T) {
	endpoint := authtest.NewTokenServer(t)
	params := directParams(t.TempDir(), endpoint)
	params.AuthConfig.ClientSecret = "config-secret"
	params.AuthConfig.TokenEndpointAuthMethod = "client_secret_basic"
	saveLoginFor(t, params, storedToken(time.Time{}))
	p, err := provider.New(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, refreshErr := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); refreshErr != nil {
		t.Fatalf("RefreshAuthorization: %v", refreshErr)
	}
	endpoint.Mu.Lock()
	pass := endpoint.LastBasicPass
	endpoint.Mu.Unlock()
	if pass != "config-secret" {
		t.Errorf("refresh sent secret %q, want config-secret", pass)
	}
	saved, err := auth.LoadLogin(params.ConfigDir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Client.Secret != "" {
		t.Errorf("saved login holds client secret %q, want none for a configured client", saved.Client.Secret)
	}
}

func TestNewProvider_concurrentConstruction_leavesSharedConfigUnchanged(t *testing.T) {
	shared := &config.AuthConfig{
		Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token",
		Scopes: []string{"read"}, ExtraAuthParams: map[string]string{"prompt": "consent"},
	}
	params := provider.Params{
		AuthConfig: shared, ConfigDir: t.TempDir(), ServerName: "srv", ServerURL: testServerURL,
		Clock: clock.NewFake(),
	}
	saveLoginFor(t, params, storedToken(time.Time{}))
	want := *shared
	want.Scopes = slices.Clone(shared.Scopes)
	want.ExtraAuthParams = maps.Clone(shared.ExtraAuthParams)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			p, err := provider.New(params)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Authorization(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(*shared, want) {
		t.Errorf("shared AuthConfig was mutated by concurrent construction: got %+v, want %+v", *shared, want)
	}
}

func TestNewProvider_nilAuthConfig_returnsError(t *testing.T) {
	_, err := provider.New(provider.Params{
		ConfigDir: t.TempDir(), ServerName: "srv", Clock: clock.NewFake(),
	})
	if err == nil {
		t.Fatal("expected error for nil AuthConfig")
	}
}

func TestNewProvider_nilClock_worksWithStoredToken(t *testing.T) {
	params := provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  t.TempDir(), ServerName: "srv", ServerURL: testServerURL,
	}
	saveLoginFor(t, params, &oauth2.Token{AccessToken: "tok", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour)})
	p, err := provider.New(params)
	if err != nil {
		t.Fatalf("NewProvider with nil Clock: %v", err)
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", got)
	}
}

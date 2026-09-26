//go:build test

package provider_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"golang.org/x/oauth2"
)

func TestNewProvider_storedRegistration_usesConfidentialClientCredentials(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "dcr-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.Srv.URL + "/token"}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.Mu.Lock()
	basicUser := endpoint.LastBasicAuth
	endpoint.Mu.Unlock()
	if basicUser != "dcr-client" {
		t.Errorf("refresh must authenticate with the registered confidential client, basic user = %q", basicUser)
	}
}

func TestNewProvider_inconsistentRegistration_returnsError(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "orphan-secret", TokenEndpointAuthMethod: "none"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"}
	if _, err := provider.New(provider.Params{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err == nil {
		t.Fatal("expected construction error for inconsistent registration when no explicit client_id")
	}
}

func TestNewProvider_noRegistration_actsAsPublicClient(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.Srv.URL + "/token"}
	p, err := provider.New(provider.Params{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("missing registration must not error: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.Mu.Lock()
	basicUser, clientID := endpoint.LastBasicAuth, endpoint.LastClientID
	endpoint.Mu.Unlock()
	if basicUser != "" || clientID != "" {
		t.Errorf("public client must not send credentials: basic=%q client_id=%q", basicUser, clientID)
	}
}

func TestNewProvider_concurrentConstruction_leavesSharedConfigUnchanged(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "dcr-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	shared := &config.AuthConfig{
		Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token",
		Scopes: []string{"read"}, ExtraAuthParams: map[string]string{"prompt": "consent"},
	}
	want := *shared
	want.Scopes = slices.Clone(shared.Scopes)
	want.ExtraAuthParams = maps.Clone(shared.ExtraAuthParams)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := provider.New(provider.Params{
				AuthConfig: shared, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake(),
			})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Authorization(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(*shared, want) {
		t.Errorf("shared AuthConfig was mutated by concurrent construction: got %+v, want %+v", *shared, want)
	}
}

func TestNewProvider_explicitClientID_ignoresStoredRegistration(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "stale-id", ClientSecret: "stale-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "manual-id", TokenURL: endpoint.Srv.URL + "/token"}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.Mu.Lock()
	gotClientID := endpoint.LastClientID
	gotBasicUser := endpoint.LastBasicAuth
	endpoint.Mu.Unlock()
	usedManualID := gotClientID == "manual-id" || gotBasicUser == "manual-id"
	if !usedManualID {
		t.Errorf("manual-id must be used; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
	}
	if gotClientID == "stale-id" || gotBasicUser == "stale-id" {
		t.Errorf("stale DCR registration must not override explicit client_id; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
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
	dir := t.TempDir()
	tok := &oauth2.Token{AccessToken: "tok", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour)}
	if err := auth.Save(dir, "srv", tok); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  dir, ServerName: "srv",
	})
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

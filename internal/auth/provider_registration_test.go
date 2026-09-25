//go:build test

package auth_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestNewProvider_storedRegistration_usesConfidentialClientCredentials(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "dcr-secret", TokenEndpointAuthMethod: "client_secret_basic"}
	if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
		t.Fatal(err)
	}
	endpoint := newMockAuthServer(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL + "/token"}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.mu.Lock()
	basicUser := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	if basicUser != "dcr-client" {
		t.Errorf("refresh must authenticate with the registered confidential client, basic user = %q", basicUser)
	}
}

func TestNewProvider_inconsistentRegistration_returnsError(t *testing.T) {
	t.Run("ignored when explicit client_id set", func(t *testing.T) {
		dir := t.TempDir()
		reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "orphan-secret", TokenEndpointAuthMethod: "none"}
		if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
			t.Fatal(err)
		}
		ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "http://localhost:1/token"}
		if _, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err != nil {
			t.Fatalf("inconsistent registration must be ignored when explicit client_id is set: %v", err)
		}
	})
	t.Run("errors when no explicit client_id", func(t *testing.T) {
		dir := t.TempDir()
		reg := &auth.Registration{ClientID: "dcr-client", ClientSecret: "orphan-secret", TokenEndpointAuthMethod: "none"}
		if err := auth.SaveRegistration(dir, "srv", reg); err != nil {
			t.Fatal(err)
		}
		ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"}
		if _, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}); err == nil {
			t.Fatal("expected construction error for inconsistent registration when no explicit client_id")
		}
	})
}

func TestNewProvider_noRegistration_actsAsPublicClient(t *testing.T) {
	dir := t.TempDir()
	endpoint := newMockAuthServer(t)
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.srv.URL + "/token"}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("missing registration must not error: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.mu.Lock()
	basicUser, clientID := endpoint.lastBasicAuth, endpoint.lastClientID
	endpoint.mu.Unlock()
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
			p, err := auth.NewProvider(auth.ProviderParams{
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
	endpoint := newMockAuthServer(t)
	ac := &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "manual-id", TokenURL: endpoint.srv.URL + "/token"}
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: ac, ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	endpoint.mu.Lock()
	gotClientID := endpoint.lastClientID
	gotBasicUser := endpoint.lastBasicAuth
	endpoint.mu.Unlock()
	usedManualID := gotClientID == "manual-id" || gotBasicUser == "manual-id"
	if !usedManualID {
		t.Errorf("manual-id must be used; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
	}
	if gotClientID == "stale-id" || gotBasicUser == "stale-id" {
		t.Errorf("stale DCR registration must not override explicit client_id; form client_id=%q, basic user=%q", gotClientID, gotBasicUser)
	}
}

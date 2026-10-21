//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

type registrationIssuer struct {
	server        *httptest.Server
	registrations atomic.Int32
}

func newRegistrationIssuer(t *testing.T, clientID string) *registrationIssuer {
	t.Helper()
	fixture := &registrationIssuer{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/register" {
			fixture.registrations.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"client_id": clientID}) //nolint:errcheck
			return
		}
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"issuer": fixture.server.URL, "authorization_endpoint": "https://shared.example/authorize",
			"token_endpoint": "https://shared.example/token", "registration_endpoint": fixture.server.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"}, "client_id_metadata_document_supported": true,
		})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func TestRegistrationIsBoundToIssuerEvenWhenTokenEndpointMatches(t *testing.T) {
	first := newRegistrationIssuer(t, "first-client")
	second := newRegistrationIssuer(t, "second-client")
	var current atomic.Pointer[string]
	current.Store(&first.server.URL)
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{*current.Load()}}) //nolint:errcheck
	}))
	t.Cleanup(resource.Close)
	dir := t.TempDir()
	resolve := func() auth.ResolvedLogin {
		sc := config.ServerConfig{
			URL:  resource.URL + "/mcp",
			Auth: &config.AuthConfig{Type: "oauth2", CallbackPort: 7777},
		}
		result, err := auth.ResolveEndpoints(t.Context(), &sc, resolveParams(dir, "srv"))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := resolve(); got.Client.ID != "first-client" {
		t.Fatalf("first client = %+v", got)
	}
	if got := resolve(); got.Client.ID != "first-client" || first.registrations.Load() != 1 {
		t.Fatalf("same issuer did not reuse registration: client=%+v registrations=%d", got, first.registrations.Load())
	}
	current.Store(&second.server.URL)
	got := resolve()
	if got.Client.ID != "second-client" || second.registrations.Load() != 1 {
		t.Fatalf("changed issuer reused credentials: client=%+v registrations=%d", got, second.registrations.Load())
	}
	saved, err := auth.LoadRegistration(dir, "srv")
	if err != nil || saved.Issuer != second.server.URL {
		t.Fatalf("saved registration = %+v, %v", saved, err)
	}
}

func TestUnboundRegistrationRequiresFreshRegistration(t *testing.T) {
	issuer := newRegistrationIssuer(t, "fresh-client")
	dir := t.TempDir()
	authtest.SaveRegistration(
		t,
		authtest.RegistrationFile{ConfigDir: dir, ServerName: "srv", Registration: &auth.Registration{
			ClientID: "unbound-client", ClientSecret: "unbound-secret", TokenURL: "https://shared.example/token",
		}},
	)
	sc := config.ServerConfig{
		URL:  issuer.server.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2", CallbackPort: 7777},
	}
	got, err := auth.ResolveEndpoints(t.Context(), &sc, resolveParams(dir, "srv"))
	if err != nil || got.Client.ID != "fresh-client" || issuer.registrations.Load() != 1 || sc.Auth.ClientSecret != "" {
		t.Fatalf(
			"unbound registration reused: resolved=%+v error=%v registrations=%d",
			got,
			err,
			issuer.registrations.Load(),
		)
	}
}

func TestConfiguredClientRequiresExactIssuerBinding(t *testing.T) {
	issuer := newRegistrationIssuer(t, "unused-client")
	for _, tc := range []struct {
		name, binding, clientID string
		wantError               bool
	}{
		{name: "missing issuer", clientID: "configured", wantError: true},
		{name: "different issuer", binding: "https://old.example", clientID: "configured", wantError: true},
		{name: "trailing slash differs", binding: issuer.server.URL + "/", clientID: "configured", wantError: true},
		{name: "matching issuer", binding: issuer.server.URL, clientID: "configured"},
		{name: "portable metadata client", clientID: auth.ClientMetadataURL},
		{name: "URL without metadata path", clientID: "https://client.example", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := config.ServerConfig{URL: issuer.server.URL + "/mcp", Auth: &config.AuthConfig{
				Type: "oauth2", Issuer: tc.binding, ClientID: tc.clientID,
				AuthURL: "https://shared.example/authorize", TokenURL: "https://shared.example/token",
			}}
			got, err := auth.ResolveEndpoints(context.Background(), &sc, resolveParams(t.TempDir(), "srv"))
			if (err != nil) != tc.wantError {
				t.Fatalf("resolved=%+v error=%v wantError=%v", got, err, tc.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), "issuer") {
				t.Fatalf("error does not identify issuer: %v", err)
			}
			if !tc.wantError && got.Issuer.Issuer != issuer.server.URL {
				t.Fatalf("callback issuer = %+v", got.Issuer)
			}
		})
	}
	if count := issuer.registrations.Load(); count != 0 {
		t.Fatalf("configured client silently re-registered %d times", count)
	}
}

func TestSavedLoginWithoutIssuerCannotBeLoadedOrRefreshed(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(
		t,
		filepath.Join(dir, "internal", "srv.token.json"),
		`{"resource":"https://resource.example/mcp","token_url":"https://shared.example/token","client":{"id":"old-client"},"token":{"access_token":"old-token","refresh_token":"old-refresh"}}`,
	)
	_, err := auth.LoadLogin(dir, "srv")
	if err == nil || !strings.Contains(err.Error(), "log in again") {
		t.Fatalf("unbound login load error = %v", err)
	}
}

func TestSavedLoginRetainsIssuerAndRejectsConfiguredIssuerChange(t *testing.T) {
	sc := config.ServerConfig{Name: "srv", URL: "https://resource.example/mcp", Auth: &config.AuthConfig{
		Type:     "oauth2",
		Issuer:   "https://issuer.example",
		ClientID: "configured",
		TokenURL: "https://shared.example/token",
	}}
	dir := t.TempDir()
	authtest.SaveToken(
		t,
		authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Server: sc, Token: &oauth2.Token{AccessToken: "token"}},
	)
	login, err := auth.LoadLoginFor(dir, sc)
	if err != nil || login.Issuer != sc.Auth.Issuer {
		t.Fatalf("saved login issuer = %+v, %v", login, err)
	}
	if got := login.RefreshConfig(sc.Auth); got.Issuer != login.Issuer {
		t.Fatalf("refresh lost issuer: %+v", got)
	}
	sc.Auth.Issuer += "/"
	if _, err := auth.LoadLoginFor(dir, sc); err == nil || !strings.Contains(err.Error(), "issuer changed") {
		t.Fatalf("changed issuer load error = %v", err)
	}
}

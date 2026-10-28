//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
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
		json.NewEncoder(w).
			Encode(map[string]any{"resource": "http://" + r.Host + "/mcp", "authorization_servers": []string{*current.Load()}})
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
		secret, authMethod      string
		wantError               bool
	}{
		{name: "missing issuer", clientID: "configured", wantError: true},
		{name: "different issuer", binding: "https://old.example", clientID: "configured", wantError: true},
		{name: "trailing slash differs", binding: issuer.server.URL + "/", clientID: "configured", wantError: true},
		{name: "matching issuer", binding: issuer.server.URL, clientID: "configured"},
		{name: "portable metadata client", clientID: auth.ClientMetadataURL},
		{name: "metadata URL with secret", clientID: auth.ClientMetadataURL, secret: "configured-secret", wantError: true},
		{name: "metadata URL with client authentication", clientID: auth.ClientMetadataURL, authMethod: "client_secret_basic", wantError: true},
		{name: "URL without metadata path", clientID: "https://client.example", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := config.ServerConfig{URL: issuer.server.URL + "/mcp", Auth: &config.AuthConfig{
				Type: "oauth2", Issuer: tc.binding, ClientID: tc.clientID,
				ClientSecret: tc.secret, TokenEndpointAuthMethod: tc.authMethod,
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

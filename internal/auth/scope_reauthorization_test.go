//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestReauthorizationScopeSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stepUp  bool
		changed string
		want    []string
	}{
		{name: "manual narrowing", want: []string{"read"}},
		{name: "permission stepup", stepUp: true, want: []string{"old", "read", "write"}},
		{name: "issuer changed", stepUp: true, changed: "issuer", want: []string{"read", "write"}},
		{name: "resource changed", stepUp: true, changed: "resource", want: []string{"read", "write"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := scopeTestServer(t)
			dir := t.TempDir()
			previous := &auth.Login{
				ServerURL: sc.URL,
				Resource:  sc.URL,
				Issuer:    sc.Auth.Issuer,
				TokenURL:  sc.Auth.TokenURL,
				Client:    auth.LoginClient{ID: "client", Configured: true},
				Scopes:    []string{"old"},
				Token:     &oauth2.Token{AccessToken: "old"},
			}
			if tc.changed == "issuer" {
				previous.Issuer = "https://old.example.test"
			}
			if tc.changed == "resource" {
				previous.Resource = sc.URL + "/different"
			}
			if err := auth.SaveLogin(dir, sc.Name, previous); err != nil {
				t.Fatal(err)
			}
			if tc.stepUp {
				if err := auth.SaveChallenge(
					dir,
					sc.Name,
					auth.Challenge{
						ServerURL:       sc.URL,
						StatusCode:      403,
						WWWAuthenticate: `Bearer error="insufficient_scope", scope="write"`,
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(dir, sc.Name)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(sc.Auth.Scopes, tc.want) {
				t.Fatalf("scopes = %v, want %v", sc.Auth.Scopes, tc.want)
			}
		})
	}
}

func scopeTestServer(t *testing.T) *config.ServerConfig {
	t.Helper()
	as := authtest.ServeASMeta(
		t,
		"/.well-known/oauth-authorization-server",
		map[string]any{
			"authorization_endpoint":           "/authorize",
			"token_endpoint":                   "/token",
			"code_challenge_methods_supported": []string{"S256"},
		},
	)
	t.Cleanup(as.Close)
	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).
				Encode(map[string]any{"resource": mcp.URL + "/mcp", "authorization_servers": []string{as.URL}}); err != nil {
				t.Error(err)
			}
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcp.Close)
	return &config.ServerConfig{
		Name: "srv",
		URL:  mcp.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "client",
			Issuer:   as.URL,
			AuthURL:  as.URL + "/authorize",
			TokenURL: as.URL + "/token",
			Scopes:   []string{"read"},
		},
	}
}

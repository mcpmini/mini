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
		status  int
		changed string
		want    []string
	}{
		{name: "manual narrowing", want: []string{"read"}},
		{name: "saved 401 excludes previous scopes", status: http.StatusUnauthorized, want: []string{"read", "write"}},
		{name: "permission stepup", status: http.StatusForbidden, want: []string{"old", "read", "write"}},
		{name: "issuer changed", status: http.StatusForbidden, changed: "issuer", want: []string{"read", "write"}},
		{name: "resource changed", status: http.StatusForbidden, changed: "resource", want: []string{"read", "write"}},
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
			if tc.status != 0 {
				if err := auth.SaveChallenge(
					dir,
					sc.Name,
					auth.Challenge{
						ServerURL:       sc.URL,
						StatusCode:      tc.status,
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

func TestResolveEndpoints_scopeChallengeReacquiresCustomPRM(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "/authorize", "token_endpoint": "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()
	var mcpSrv *httptest.Server
	mcpSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/mcp" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/custom-prm" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"resource": mcpSrv.URL + "/mcp", "authorization_servers": []string{asSrv.URL},
			})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp", resource_metadata="`+mcpSrv.URL+`/custom-prm"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()
	dir := t.TempDir()
	if err := auth.SaveChallenge(dir, "srv", auth.Challenge{
		ServerURL: mcpSrv.URL + "/mcp", StatusCode: http.StatusForbidden,
		WWWAuthenticate: `Bearer error="insufficient_scope", scope="mcp:write"`,
	}); err != nil {
		t.Fatal(err)
	}
	sc := &config.ServerConfig{Name: "srv", URL: mcpSrv.URL + "/mcp", Auth: &config.AuthConfig{
		Type: config.AuthTypeOAuth2, ClientID: "client", Issuer: asSrv.URL, Scopes: []string{"mcp:configured"},
		AuthURL: asSrv.URL + "/authorize", TokenURL: asSrv.URL + "/token",
	}}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(dir, "srv")); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.Issuer != asSrv.URL || sc.Auth.ResourceURL != sc.URL {
		t.Fatalf("custom PRM discovery: issuer = %q, resource = %q", sc.Auth.Issuer, sc.Auth.ResourceURL)
	}
}

func TestResolveEndpoints_doesNotReuseChallengeOrScopesAfterURLAndIssuerChange(t *testing.T) {
	oldAS := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://old-as.example.test/authorize",
		"token_endpoint":                   "https://old-as.example.test/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer oldAS.Close()
	newAS := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://new-as.example.test/authorize",
		"token_endpoint":                   "https://new-as.example.test/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer newAS.Close()
	oldMCP := httptest.NewServer(http.NotFoundHandler())
	defer oldMCP.Close()
	var newMCP *httptest.Server
	newMCP = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/new-prm" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"resource": newMCP.URL + "/mcp", "authorization_servers": []string{newAS.URL},
			})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+newMCP.URL+`/new-prm", scope="mcp:write"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer newMCP.Close()
	dir := t.TempDir()
	oldURL := oldMCP.URL + "/mcp"
	if err := auth.SaveLogin(dir, "srv", &auth.Login{
		Resource: oldURL, ServerURL: oldURL, Issuer: oldAS.URL, TokenURL: oldAS.URL + "/token",
		Client: auth.LoginClient{ID: "client", Configured: true}, Scopes: []string{"mcp:secret"},
		Token: &oauth2.Token{AccessToken: "old-token"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveChallenge(dir, "srv", auth.Challenge{
		ServerURL: oldURL, StatusCode: http.StatusUnauthorized,
		WWWAuthenticate: `Bearer resource_metadata="https://old.example.test/prm"`,
	}); err != nil {
		t.Fatal(err)
	}
	sc := &config.ServerConfig{Name: "srv", URL: newMCP.URL + "/mcp", Auth: &config.AuthConfig{
		Type: config.AuthTypeOAuth2, ClientID: "client", Issuer: newAS.URL,
		AuthURL: newAS.URL + "/authorize", TokenURL: newAS.URL + "/token",
	}}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(dir, "srv")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sc.Auth.Scopes, []string{"mcp:write"}) {
		t.Fatalf("scopes after server identity change = %v, want [mcp:write]", sc.Auth.Scopes)
	}
}

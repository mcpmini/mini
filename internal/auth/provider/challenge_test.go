//go:build test

package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestHTTP403PersistsScopeChallengeThroughProvider(t *testing.T) {
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Add("WWW-Authenticate", `Basic realm="ignored"`)
		w.Header().Add("WWW-Authenticate", `Bearer error="insufficient_scope", scope="mcp:write"`)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer mcp.Close()
	dir := t.TempDir()
	clk := clock.NewFake()
	sc := config.ServerConfig{
		Name: "srv", URL: mcp.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: "https://as.example.test/token",
		},
	}
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: sc.Name, Server: sc,
		Token: &oauth2.Token{AccessToken: "access", TokenType: "Bearer"},
	})
	p, err := provider.New(provider.Params{
		AuthConfig: sc.Auth, ConfigDir: dir, ServerName: sc.Name, ServerURL: sc.URL, Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.NewHTTPConnection(transport.HTTPConnectionConfig{
		URL: sc.URL, Clock: clk, ServerName: sc.Name,
		AuthProvider: p, AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, listErr := conn.ListTools(context.Background()); listErr == nil {
		t.Fatal("ListTools succeeded despite the scope challenge")
	}
	got, err := auth.LoadChallenge(dir, sc.Name, sc.URL)
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := `Basic realm="ignored", Bearer error="insufficient_scope", scope="mcp:write"`
	if got.StatusCode != http.StatusForbidden || got.WWWAuthenticate != wantHeader {
		t.Fatalf("stored challenge = %#v", got)
	}
}

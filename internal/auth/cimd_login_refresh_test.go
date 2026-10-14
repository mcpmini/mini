//go:build test

package auth_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestCIMDBrowserLogin_refreshUsesTheIssuingClient(t *testing.T) {
	sc, endpoint, login := startCIMDRefreshLogin(t)
	authtest.CompleteAuthorization(t, login.AuthURL(), "code")
	token, err := login.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	commitAndRefreshCIMDToken(t, sc, token)
	endpoint.Mu.Lock()
	form, basic := endpoint.LastClientID, endpoint.LastBasicAuth
	endpoint.Mu.Unlock()
	if form != auth.ClientMetadataURL && basic != url.QueryEscape(auth.ClientMetadataURL) {
		t.Fatalf("refresh client_id = form:%q basic:%q, want issuing client %q", form, basic, auth.ClientMetadataURL)
	}
}

func startCIMDRefreshLogin(t *testing.T) (config.ServerConfig, *authtest.TokenServer, *auth.BrowserLogin) {
	t.Helper()
	auth.UseEphemeralCallbackPort()
	endpoint := authtest.NewTokenServer(t)
	metadata := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": endpoint.Srv.URL + "/authorize", "token_endpoint": endpoint.Srv.URL + "/token",
		"client_id_metadata_document_supported": true, "code_challenge_methods_supported": []string{"S256"},
	})
	t.Cleanup(metadata.Close)
	sc := config.ServerConfig{
		Name: "srv",
		URL:  metadata.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			AuthURL:  endpoint.Srv.URL + "/authorize",
			TokenURL: endpoint.Srv.URL + "/token",
		},
	}
	login, err := auth.BeginLogin(
		context.Background(),
		sc,
		auth.BeginLoginParams{ConfigDir: t.TempDir(), ServerName: sc.Name, Clock: clock.NewFake()},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = login.Close() })
	return sc, endpoint, login
}

func commitAndRefreshCIMDToken(t *testing.T, sc config.ServerConfig, token *auth.Login) {
	t.Helper()
	registry := provider.NewRegistry()
	defer registry.Close()
	params := provider.Params{
		AuthConfig: sc.Auth,
		ConfigDir:  t.TempDir(),
		ServerName: sc.Name,
		ServerURL:  sc.URL,
		Clock:      clock.NewFake(),
	}
	if err := registry.CommitAuthorizedToken(params, token); err != nil {
		t.Fatal(err)
	}
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(t.Context(), "Bearer "+token.Token.AccessToken); err != nil {
		t.Fatal(err)
	}
}

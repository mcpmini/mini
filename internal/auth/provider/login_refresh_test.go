//go:build test

package provider_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestIssuerBindingSurvivesBrowserLoginCommitAndRefresh(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	endpoint := authtest.NewTokenServer(t)
	dir := t.TempDir()
	sc := config.ServerConfig{Name: "srv", URL: endpoint.Srv.URL + "/mcp", Auth: endpoint.AuthConfig()}
	browser, err := auth.BeginLogin(
		t.Context(),
		sc,
		auth.BeginLoginParams{ConfigDir: dir, ServerName: sc.Name, Clock: clock.System()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close() //nolint:errcheck // Close always returns nil
	authtest.CompleteAuthorization(t, browser.AuthURL(), "test-code")
	login, err := browser.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	params := provider.Params{
		AuthConfig: sc.Auth,
		ConfigDir:  dir,
		ServerName: sc.Name,
		ServerURL:  sc.URL,
		Clock:      clock.System(),
	}
	registry := provider.NewRegistry()
	t.Cleanup(registry.Close)
	if commitErr := registry.CommitAuthorizedToken(params, login); commitErr != nil {
		t.Fatal(commitErr)
	}
	p, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, refreshErr := p.RefreshAuthorization(t.Context(), "Bearer "+login.Token.AccessToken); refreshErr != nil {
		t.Fatal(refreshErr)
	}
	saved, err := auth.LoadLoginFor(dir, sc)
	if err != nil || saved.Issuer != endpoint.Srv.URL {
		t.Fatalf("refreshed issuer = %+v, error=%v", saved, err)
	}
	if hits := endpoint.Hits.Load(); hits != 2 {
		t.Fatalf("token calls = %d, want exchange and refresh", hits)
	}
}

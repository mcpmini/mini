//go:build test

package provider_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestUnboundSavedLoginNeverReachesRefreshEndpoint(t *testing.T) {
	endpoint := authtest.NewTokenServer(t)
	params := directParams(t.TempDir(), endpoint)
	login := loginFor(t, params, storedToken(time.Time{}))
	login.Issuer = ""
	raw, err := json.Marshal(login)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(params.ConfigDir, "internal", "srv.token.json"), string(raw))
	p, err := provider.New(params)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil || !strings.Contains(err.Error(), "log in again") {
		t.Fatalf("unbound refresh error = %v", err)
	}
	if hits := endpoint.Hits.Load(); hits != 0 {
		t.Fatalf("unbound login reached token endpoint %d times", hits)
	}
}

func TestCommitRejectsLoginForAnotherIssuer(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	params.AuthConfig.Issuer = "https://new.example"
	login := loginFor(t, params, storedToken(time.Time{}))
	login.Issuer = "https://old.example"
	if err := f.registry.CommitAuthorizedToken(
		params,
		login,
	); err == nil ||
		!strings.Contains(err.Error(), "issuer changed") {
		t.Fatalf("mismatched issuer commit error = %v", err)
	}
}

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

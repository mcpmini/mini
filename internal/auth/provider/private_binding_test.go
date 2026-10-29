//go:build test

package provider_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestPrivateRefreshBindingMismatchRequiresReauthorizationWithoutRetry(t *testing.T) {
	for _, mismatch := range []string{"issuer", "token", "resource"} {
		t.Run(mismatch, func(t *testing.T) {
			var hits, tokens atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/mcp":
					w.WriteHeader(http.StatusUnauthorized)
				case "/.well-known/oauth-authorization-server":
					origin := "http://" + r.Host
					w.Write(
						[]byte(
							`{"issuer":"` + origin + `","authorization_endpoint":"` + origin + `/authorize","token_endpoint":"` + origin + `/token","code_challenge_methods_supported":["S256"]}`,
						),
					)
				case "/token":
					tokens.Add(1)
					w.Write([]byte(`{"access_token":"unexpected"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer source.Close()
			dir := t.TempDir()
			clk := clock.NewFake()
			login := &auth.Login{
				Resource:  source.URL + "/mcp",
				ServerURL: source.URL + "/mcp",
				Issuer:    source.URL,
				TokenURL:  source.URL + "/token",
				Client:    auth.LoginClient{ID: "cid", Configured: true},
				Token:     storedToken(clk.Now()),
			}
			switch mismatch {
			case "issuer":
				login.Issuer = "https://old.example"
			case "resource":
				login.Resource = source.URL + "/old"
			default:
				login.TokenURL = source.URL + "/old-token"
			}
			if err := auth.SaveLogin(dir, "srv", login); err != nil {
				t.Fatal(err)
			}
			p, err := provider.New(
				provider.Params{
					ConfigDir:  dir,
					ServerName: "srv",
					ServerURL:  source.URL + "/mcp",
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
					Clock:      clk,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.RefreshAuthorization(t.Context(), "Bearer stored-access")
			if !errors.Is(err, transport.ErrReauthRequired) || !strings.Contains(err.Error(), "mini auth srv") {
				t.Fatalf("missing reauthorization remedy: %v", err)
			}
			first := hits.Load()
			if first == 0 {
				t.Fatal("refresh did not rediscover binding")
			}
			for range 2 {
				if _, err := p.RefreshAuthorization(
					t.Context(),
					"Bearer stored-access",
				); !errors.Is(
					err,
					transport.ErrReauthRequired,
				) {
					t.Fatal(err)
				}
			}
			if hits.Load() != first || tokens.Load() != 0 {
				t.Fatalf("retried permanent mismatch: requests %d -> %d, tokens %d", first, hits.Load(), tokens.Load())
			}
		})
	}
}

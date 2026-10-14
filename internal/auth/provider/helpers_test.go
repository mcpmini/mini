//go:build test

package provider_test

import (
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type providerFixture struct {
	dir      string
	endpoint *authtest.TokenServer
	clock    *clock.Fake
	provider transport.AuthorizationProvider
}

type providerSetup struct {
	Token *oauth2.Token
	Auth  *config.AuthConfig
}

func newProviderFixture(t *testing.T, s providerSetup) *providerFixture {
	t.Helper()
	f := &providerFixture{dir: t.TempDir(), endpoint: authtest.NewTokenServer(t), clock: clock.NewFake()}
	f.endpoint.AccessToken = "new-access"
	f.endpoint.RefreshToken = "rotated-refresh"
	if s.Auth == nil {
		s.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"}
	}
	s.Auth.TokenURL = f.endpoint.Srv.URL + "/token"
	resourceURL := "https://resource.example.com/mcp"
	if s.Token != nil {
		authtest.SaveToken(
			t,
			authtest.TokenFile{
				ConfigDir:  f.dir,
				ServerName: "srv",
				Resource:   resourceURL,
				TokenURL:   s.Auth.TokenURL,
				Client:     auth.Client{Source: auth.ClientConfigured, ID: s.Auth.ClientID},
				Token:      s.Token,
			},
		)
	}
	p, err := provider.New(provider.Params{
		AuthConfig: s.Auth, ConfigDir: f.dir, ServerName: "srv", ServerURL: resourceURL, Clock: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.provider = p
	return f
}

func storedToken(expiry time.Time) *oauth2.Token {
	return &oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: expiry}
}

func gateNextTokenRequest(t *testing.T, m *authtest.TokenServer) (received <-chan struct{}, release func()) {
	ready := make(chan struct{})
	gate := make(chan struct{})
	m.Mu.Lock()
	m.HoldReady = ready
	m.HoldGate = gate
	m.Mu.Unlock()
	release = sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release) // a test failing before release would otherwise hang the server's Close
	return ready, release
}

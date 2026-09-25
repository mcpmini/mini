//go:build test

package auth_test

import (
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type providerFixture struct {
	dir      string
	endpoint *mockAuthServer
	clock    *clock.Fake
	provider transport.AuthorizationProvider
}

type providerSetup struct {
	Token *oauth2.Token
	Auth  *config.AuthConfig
}

func newProviderFixture(t *testing.T, s providerSetup) *providerFixture {
	t.Helper()
	f := &providerFixture{dir: t.TempDir(), endpoint: newMockAuthServer(t), clock: clock.NewFake()}
	f.endpoint.accessToken = "new-access"
	f.endpoint.refreshToken = "rotated-refresh"
	if s.Auth == nil {
		s.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"}
	}
	s.Auth.TokenURL = f.endpoint.srv.URL + "/token"
	if s.Token != nil {
		if err := auth.Save(f.dir, "srv", s.Token); err != nil {
			t.Fatal(err)
		}
	}
	p, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: s.Auth, ConfigDir: f.dir, ServerName: "srv", Clock: f.clock,
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

// holdMockServer installs a one-shot gate on m: handleToken blocks after
// receiving the request until the returned release function is called.
func holdMockServer(m *mockAuthServer) (received <-chan struct{}, release func()) {
	ready := make(chan struct{})
	gate := make(chan struct{})
	m.mu.Lock()
	m.holdReady = ready
	m.holdGate = gate
	m.mu.Unlock()
	return ready, func() { close(gate) }
}

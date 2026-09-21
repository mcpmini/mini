//go:build test

package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type tokenEndpoint struct {
	srv           *httptest.Server
	hits          atomic.Int32
	status        atomic.Int32
	mu            sync.Mutex
	accessToken   string
	refreshToken  string
	lastGrant     string
	lastRefresh   string
	lastBasicAuth string
	lastClientID  string
	lastResource  string
}

func newTokenEndpoint(t *testing.T) *tokenEndpoint {
	t.Helper()
	e := &tokenEndpoint{accessToken: "new-access", refreshToken: "rotated-refresh"}
	e.status.Store(http.StatusOK)
	e.srv = httptest.NewServer(http.HandlerFunc(e.handle))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *tokenEndpoint) handle(w http.ResponseWriter, r *http.Request) {
	e.hits.Add(1)
	if status := int(e.status.Load()); status != http.StatusOK {
		http.Error(w, "refresh rejected", status)
		return
	}
	r.ParseForm() //nolint:errcheck
	e.mu.Lock()
	e.lastGrant = r.FormValue("grant_type")
	e.lastRefresh = r.FormValue("refresh_token")
	e.lastClientID = r.FormValue("client_id")
	e.lastResource = r.FormValue("resource")
	user, _, ok := r.BasicAuth()
	if ok {
		e.lastBasicAuth = user
	}
	access, refresh := e.accessToken, e.refreshToken
	e.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token": access, "refresh_token": refresh,
		"token_type": "Bearer", "expires_in": 3600,
	})
}

type providerFixture struct {
	dir      string
	endpoint *tokenEndpoint
	clock    *clock.Fake
	provider transport.AuthorizationProvider
}

type providerSetup struct {
	Token *oauth2.Token
	Auth  *config.AuthConfig
}

func newProviderFixture(t *testing.T, s providerSetup) *providerFixture {
	t.Helper()
	f := &providerFixture{dir: t.TempDir(), endpoint: newTokenEndpoint(t), clock: clock.NewFake()}
	if s.Auth == nil {
		s.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"}
	}
	s.Auth.TokenURL = f.endpoint.srv.URL
	if s.Token != nil {
		if err := auth.Save(f.dir, "srv", s.Token); err != nil {
			t.Fatal(err)
		}
	}
	p, err := auth.NewProvider(auth.ProviderParams{AuthConfig: s.Auth, ConfigDir: f.dir, ServerName: "srv", Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	f.provider = p
	return f
}

func storedToken(expiry time.Time) *oauth2.Token {
	return &oauth2.Token{AccessToken: "stored-access", RefreshToken: "stored-refresh", Expiry: expiry}
}

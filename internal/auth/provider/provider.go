package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type tokenProvider struct {
	server    config.ServerConfig
	configDir string
	clock     clock.Clock
	lifetime  context.Context

	mu               sync.Mutex
	login            *auth.Login
	persistedLogin   *auth.Login
	proactiveRetryAt time.Time
	deadRefreshToken string
	deadRefreshErr   error
	retired          bool
}

func New(p Params) (transport.AuthorizationProvider, error) {
	return buildTokenProvider(p)
}

func (p *tokenProvider) Authorization(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureLoginLocked(); err != nil {
		return "", err
	}
	if p.shouldRefreshLocked() {
		//nolint:contextcheck // The provider mutex serializes rotation; persist it beyond this caller's cancellation.
		if err := p.proactiveRefreshLocked(); err != nil {
			return "", err
		}
	}
	return bearerValue(p.login.Token), nil
}

func (p *tokenProvider) RefreshAuthorization(ctx context.Context, stale string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureLoginLocked(); err != nil {
		return "", err
	}
	if bearerValue(p.login.Token) != stale {
		return bearerValue(p.login.Token), nil
	}
	//nolint:contextcheck // The provider mutex serializes rotation; persist it beyond this caller's cancellation.
	if err := p.refreshLocked(func() bool { return bearerValue(p.login.Token) == stale }); err != nil {
		return "", err
	}
	return bearerValue(p.login.Token), nil
}

var errRetired = errors.New("its server was removed")

func (p *tokenProvider) retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retired = true
	p.login = nil
}

func (p *tokenProvider) ensureLoginLocked() error {
	if p.retired {
		return fmt.Errorf("%s: %w", p.server.Name, errRetired)
	}
	if p.login != nil {
		return nil
	}
	login, err := p.loadSavedLogin()
	if err != nil {
		return err
	}
	p.adoptLoginLocked(login)
	return nil
}

func (p *tokenProvider) loadSavedLogin() (*auth.Login, error) {
	login, err := auth.LoadLoginFor(p.configDir, p.server)
	if auth.IsNotFound(err) {
		return nil, transport.NotLoggedInError(p.server.Name)
	}
	if err != nil {
		return nil, p.remedyError(err)
	}
	return login, nil
}

func (p *tokenProvider) remedyError(cause error) error {
	return transport.ReauthorizationError(p.server.Name, cause)
}

// commitBrowserLogin takes up the settings the login was made with: a login started after editing
// client_id or token_url is made for the edited config, which this provider would otherwise refuse.
func (p *tokenProvider) commitBrowserLogin(server config.ServerConfig, login *auth.Login) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired {
		return fmt.Errorf("%s: %w", p.server.Name, errRetired)
	}
	if err := auth.SaveLogin(p.configDir, p.server.Name, login); err != nil {
		return fmt.Errorf("persist oauth login: %w", err)
	}
	p.server = server
	p.adoptLoginLocked(login)
	return nil
}

func bearerValue(t *oauth2.Token) string {
	return "Bearer " + t.AccessToken
}

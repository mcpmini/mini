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
	ac                     *config.AuthConfig
	preHydrationAuthConfig *config.AuthConfig
	configDir              string
	serverName             string
	serverURL              string
	clock                  clock.Clock
	lifetime               context.Context

	mu               sync.Mutex
	token            *oauth2.Token
	persistedToken   *oauth2.Token
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
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if p.shouldRefreshLocked() {
		p.reloadPersistedTokenLocked(p.loadUnlocked)
	}
	if p.shouldRefreshLocked() && !p.inProactiveBackoffLocked() {
		//nolint:contextcheck // The provider mutex serializes rotation; persist it beyond this caller's cancellation.
		if err := p.proactiveRefreshLocked(); err != nil {
			return "", err
		}
	}
	return bearerValue(p.token), nil
}

func (p *tokenProvider) RefreshAuthorization(ctx context.Context, stale string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if bearerValue(p.token) != stale {
		return bearerValue(p.token), nil
	}
	p.reloadPersistedTokenLocked(p.loadUnlocked)
	if bearerValue(p.token) != stale {
		return bearerValue(p.token), nil
	}
	stillStale := func() bool { return bearerValue(p.token) == stale }
	//nolint:contextcheck // The provider mutex serializes rotation; persist it beyond this caller's cancellation.
	if err := p.refreshLocked(stillStale); err != nil {
		return "", err
	}
	return bearerValue(p.token), nil
}

var errRetired = errors.New("its server was removed")

func (p *tokenProvider) retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retired = true
	p.token = nil
}

func (p *tokenProvider) ensureTokenLocked() error {
	if p.retired {
		return fmt.Errorf("%s: %w", p.serverName, errRetired)
	}
	if p.token != nil {
		return nil
	}
	t, err := auth.Load(p.configDir, p.serverName)
	if auth.IsNotFound(err) {
		return transport.NotLoggedInError(p.serverName)
	}
	if err != nil {
		return p.remedyError(fmt.Errorf("load token: %w", err))
	}
	p.token = t
	p.persistedToken = cloneToken(t)
	return nil
}

// loadUnlocked is enough to adopt what another process saved; refreshLocked reads again under the
// token lock before it exchanges anything.
func (p *tokenProvider) loadUnlocked() (*oauth2.Token, error) {
	return auth.Load(p.configDir, p.serverName)
}

func (p *tokenProvider) remedyError(cause error) error {
	return transport.ReauthorizationError(p.serverName, cause)
}

func (p *tokenProvider) commitBrowserToken(hydrated *config.AuthConfig, tok *oauth2.Token) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired {
		return fmt.Errorf("%s: %w", p.serverName, errRetired)
	}
	if err := p.tokenLock().Save(p.lifetime, tok); err != nil {
		return fmt.Errorf("persist oauth token: %w", err)
	}
	p.ac = hydrated
	p.token = tok
	p.resetRefreshStateLocked()
	p.persistedToken = cloneToken(tok)
	return nil
}

func bearerValue(t *oauth2.Token) string {
	return "Bearer " + t.AccessToken
}

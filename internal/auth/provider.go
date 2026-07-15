package auth

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// refreshSkew is how far ahead of expiry Authorization proactively refreshes.
// now >= Expiry-refreshSkew triggers a refresh (equality refreshes).
const refreshSkew = 2 * time.Minute

type ProviderParams struct {
	AuthConfig *config.AuthConfig
	ConfigDir  string
	ServerName string
	Clock      clock.Clock
}

// NewProvider builds an AuthorizationProvider for an OAuth2 server. It applies
// any persisted DCR client registration to p.AuthConfig at construction so
// confidential-client refreshes (see register.go) send the right client_secret.
// A missing registration file means a public client, not an error; an
// inconsistent one does error, since silently falling back to public-client
// auth would send requests the authorization server never agreed to.
func NewProvider(p ProviderParams) (transport.AuthorizationProvider, error) {
	if err := hydrateFromRegistration(p); err != nil {
		return nil, err
	}
	return &tokenProvider{
		ac:         p.AuthConfig,
		configDir:  p.ConfigDir,
		serverName: p.ServerName,
		clock:      p.Clock,
	}, nil
}

func hydrateFromRegistration(p ProviderParams) error {
	reg, err := LoadRegistration(p.ConfigDir, p.ServerName)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load client registration for %s: %w", p.ServerName, err)
	}
	return applyRegistration(p.AuthConfig, reg, p.Clock.Now())
}

type tokenProvider struct {
	ac         *config.AuthConfig
	configDir  string
	serverName string
	clock      clock.Clock

	mu    sync.Mutex
	token *oauth2.Token
}

func (p *tokenProvider) Authorization(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if p.shouldRefreshLocked() {
		if err := p.refreshLocked(ctx); err != nil {
			return "", err
		}
	}
	return bearerValue(p.token), nil
}

func (p *tokenProvider) RefreshAuthorization(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if err := p.refreshLocked(ctx); err != nil {
		return "", err
	}
	return bearerValue(p.token), nil
}

func (p *tokenProvider) ensureTokenLocked() error {
	if p.token != nil {
		return nil
	}
	t, err := Load(p.configDir, p.serverName)
	if err != nil {
		return p.remedyError(fmt.Errorf("load token: %w", err))
	}
	p.token = t
	return nil
}

func (p *tokenProvider) shouldRefreshLocked() bool {
	if p.token.Expiry.IsZero() || p.token.RefreshToken == "" {
		return false
	}
	return !p.clock.Now().Before(p.token.Expiry.Add(-refreshSkew))
}

func (p *tokenProvider) refreshLocked(ctx context.Context) error {
	// Clearing AccessToken on a copy forces oauth2's reuseTokenSource to hit the
	// token endpoint: it judges validity by the system clock with only a 10s
	// delta, so a token inside our 2m skew (or one the upstream just 401'd)
	// would otherwise be returned unchanged without a refresh.
	stale := *p.token
	stale.AccessToken = ""
	refreshed, err := Refresh(ctx, p.ac, &stale)
	if err != nil {
		return p.remedyError(fmt.Errorf("refresh token: %w", err))
	}
	p.token = refreshed
	if err := Save(p.configDir, p.serverName, refreshed); err != nil {
		slog.Warn("persist refreshed oauth token failed; using refreshed token in memory", "server", p.serverName, "err", err)
	}
	return nil
}

func (p *tokenProvider) remedyError(cause error) error {
	return fmt.Errorf("%s requires re-authorization; run `mini auth %s`: %w", p.serverName, p.serverName, cause)
}

func bearerValue(t *oauth2.Token) string {
	return "Bearer " + t.AccessToken
}

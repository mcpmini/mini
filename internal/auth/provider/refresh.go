package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
)

const refreshBeforeExpiry = 5 * time.Minute

// refreshTimeout is short because callers queued on a hung token endpoint each wait it out in turn.
const refreshTimeout = 10 * time.Second

const proactiveRefreshBackoff = 30 * time.Second

func (p *tokenProvider) shouldRefreshLocked() bool {
	if p.token.Expiry.IsZero() || p.token.RefreshToken == "" {
		return false
	}
	skew := refreshBeforeExpiry
	const maxBoundedLifetime = int64(refreshBeforeExpiry/time.Second) * 10
	if p.token.ExpiresIn > 0 && p.token.ExpiresIn <= maxBoundedLifetime {
		skew = time.Duration(p.token.ExpiresIn) * time.Second / 10
	}
	return !p.clock.Now().Before(p.token.Expiry.Add(-skew))
}

func (p *tokenProvider) tokenExpiredLocked() bool {
	return !p.token.Expiry.IsZero() && !p.clock.Now().Before(p.token.Expiry)
}

func (p *tokenProvider) inProactiveBackoffLocked() bool {
	return !p.tokenExpiredLocked() && p.clock.Now().Before(p.proactiveRetryAt)
}

func (p *tokenProvider) proactiveRefreshLocked() error {
	err := p.refreshLocked(p.shouldRefreshLocked)
	if err == nil {
		return nil
	}
	if p.tokenExpiredLocked() {
		return err
	}
	p.proactiveRetryAt = p.clock.Now().Add(proactiveRefreshBackoff)
	slog.Warn("proactive refresh failed; serving expiring token", "server", p.serverName, "err", err)
	return nil
}

func (p *tokenProvider) refreshLocked(stillNeeded func() bool) error {
	// The lock wait counts against refreshTimeout too, since callers queue on p.mu behind it.
	refreshCtx, cancel := context.WithTimeout(p.lifetime, refreshTimeout)
	defer cancel()
	return p.tokenLock().Do(refreshCtx, func(f auth.TokenFile) error {
		p.reloadPersistedTokenLocked(f.Load)
		if !stillNeeded() {
			return nil
		}
		return p.exchangeAndPersistLocked(refreshCtx, f)
	})
}

func (p *tokenProvider) exchangeAndPersistLocked(ctx context.Context, f auth.TokenFile) error {
	if p.token.RefreshToken == "" {
		return p.remedyError(fmt.Errorf("no refresh token stored for %s", p.serverName))
	}
	if p.token.RefreshToken == p.deadRefreshToken {
		return p.deadRefreshErr
	}
	if err := p.maybeDiscoverAndApplyLocked(ctx); err != nil {
		return err
	}
	refreshed, err := p.exchangeRefreshTokenLocked(ctx)
	if err != nil {
		return p.refreshFailureLocked(err)
	}
	p.token = refreshed
	p.resetRefreshStateLocked()
	p.persistRefreshedToken(f, refreshed)
	return nil
}

func (p *tokenProvider) refreshFailureLocked(err error) error {
	switch {
	case tokenEndpointGone(err):
		// Not marked dead: a 404/410 doesn't prove the refresh token is invalid.
		return p.remedyError(fmt.Errorf("token endpoint gone: %w", err))
	case refreshNeedsReauth(err):
		p.deadRefreshToken = p.token.RefreshToken
		p.deadRefreshErr = p.remedyError(fmt.Errorf("refresh token: %w", err))
		return p.deadRefreshErr
	}
	return fmt.Errorf("%s: token refresh failed (transient): %w", p.serverName, err)
}

func (p *tokenProvider) tokenLock() auth.TokenLock {
	return auth.TokenLock{ConfigDir: p.configDir, ServerName: p.serverName, Clock: p.clock}
}

func (p *tokenProvider) exchangeRefreshTokenLocked(ctx context.Context) (*oauth2.Token, error) {
	// oauth2's reuseTokenSource returns any token still valid by the system clock without refreshing it.
	stale := *p.token
	stale.AccessToken = ""
	return auth.Refresh(ctx, p.ac, &stale)
}

func (p *tokenProvider) persistRefreshedToken(f auth.TokenFile, refreshed *oauth2.Token) {
	if err := f.Save(refreshed); err != nil {
		slog.Warn(
			"persist refreshed oauth token failed; using refreshed token in memory",
			"server",
			p.serverName,
			"err",
			err,
		)
		return
	}
	p.persistedToken = cloneToken(refreshed)
}

func (p *tokenProvider) reloadPersistedTokenLocked(load func() (*oauth2.Token, error)) {
	t, err := load()
	if err != nil || samePersistedToken(t, p.persistedToken) {
		return
	}
	p.token = t
	p.resetRefreshStateLocked()
	p.persistedToken = cloneToken(t)
	p.rehydrateAuthConfigLocked()
}

func (p *tokenProvider) rehydrateAuthConfigLocked() {
	params := Params{
		AuthConfig: p.preHydrationAuthConfig,
		ConfigDir:  p.configDir,
		ServerName: p.serverName,
		Clock:      p.clock,
	}
	_, hydrated, err := resolveAuthConfigs(params)
	if err != nil {
		slog.Warn("rehydrate OAuth config after token adoption failed; keeping existing config",
			"server", p.serverName, "err", err)
		return
	}
	p.ac = hydrated
}

func samePersistedToken(a, b *oauth2.Token) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AccessToken == b.AccessToken && a.TokenType == b.TokenType &&
		a.RefreshToken == b.RefreshToken && a.Expiry.Equal(b.Expiry) && a.ExpiresIn == b.ExpiresIn
}

func cloneToken(t *oauth2.Token) *oauth2.Token {
	if t == nil {
		return nil
	}
	clone := *t
	return &clone
}

func (p *tokenProvider) resetRefreshStateLocked() {
	p.proactiveRetryAt = time.Time{}
	p.deadRefreshToken = ""
	p.deadRefreshErr = nil
}

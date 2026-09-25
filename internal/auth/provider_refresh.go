package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"
)

const refreshBeforeExpiry = 5 * time.Minute

// refreshTimeout is short because callers queued on a hung token endpoint each wait it out in turn.
const refreshTimeout = 10 * time.Second

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

func (p *tokenProvider) proactiveRefreshLocked() error {
	err := p.refreshLocked()
	if err == nil {
		return nil
	}
	if p.tokenExpiredLocked() {
		return err
	}
	slog.Warn("proactive refresh failed; serving expiring token", "server", p.serverName, "err", err)
	return nil
}

func (p *tokenProvider) refreshLocked() error {
	refreshCtx, cancel := context.WithTimeout(p.lifetime, refreshTimeout)
	defer cancel()
	if p.ac.TokenURL == "" {
		return p.remedyError(errors.New("no token endpoint configured"))
	}
	// oauth2's reuseTokenSource returns any token still valid by the system clock without refreshing it.
	stale := *p.token
	stale.AccessToken = ""
	refreshed, err := Refresh(refreshCtx, p.ac, &stale)
	if err != nil {
		return p.remedyError(fmt.Errorf("refresh token: %w", err))
	}
	p.token = refreshed
	p.persistRefreshedToken(refreshed)
	return nil
}

func (p *tokenProvider) persistRefreshedToken(refreshed *oauth2.Token) {
	if err := Save(p.configDir, p.serverName, refreshed); err != nil {
		slog.Warn("persist refreshed oauth token failed; using refreshed token in memory", "server", p.serverName, "err", err)
		return
	}
	p.persistedToken = cloneToken(refreshed)
}

func (p *tokenProvider) reloadPersistedTokenLocked() {
	t, err := Load(p.configDir, p.serverName)
	if err != nil || samePersistedToken(t, p.persistedToken) {
		return
	}
	p.token = t
	p.persistedToken = cloneToken(t)
	p.rehydrateAuthConfigLocked()
}

func (p *tokenProvider) rehydrateAuthConfigLocked() {
	params := ProviderParams{
		AuthConfig: cloneAuthConfig(p.preHydrationAuthConfig),
		ConfigDir:  p.configDir,
		ServerName: p.serverName,
		Clock:      p.clock,
	}
	if err := hydrateFromRegistration(params); err != nil {
		slog.Warn("rehydrate OAuth config after token adoption failed; keeping existing config",
			"server", p.serverName, "err", err)
		return
	}
	p.ac = params.AuthConfig
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

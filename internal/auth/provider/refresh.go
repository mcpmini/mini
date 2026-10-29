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
	if p.login.Token.Expiry.IsZero() || p.login.Token.RefreshToken == "" {
		return false
	}
	skew := refreshBeforeExpiry
	const maxBoundedLifetime = int64(refreshBeforeExpiry/time.Second) * 10
	if p.login.Token.ExpiresIn > 0 && p.login.Token.ExpiresIn <= maxBoundedLifetime {
		skew = time.Duration(p.login.Token.ExpiresIn) * time.Second / 10
	}
	return !p.clock.Now().Before(p.login.Token.Expiry.Add(-skew))
}

func (p *tokenProvider) tokenExpiredLocked() bool {
	return !p.login.Token.Expiry.IsZero() && !p.clock.Now().Before(p.login.Token.Expiry)
}

func (p *tokenProvider) inProactiveBackoffLocked() bool {
	return !p.tokenExpiredLocked() && p.clock.Now().Before(p.proactiveRetryAt)
}

func (p *tokenProvider) proactiveRefreshLocked() error {
	err := p.refreshLocked(func() bool { return p.shouldRefreshLocked() && !p.inProactiveBackoffLocked() })
	if err == nil {
		return nil
	}
	if p.tokenExpiredLocked() {
		return err
	}
	p.proactiveRetryAt = p.clock.Now().Add(proactiveRefreshBackoff)
	slog.Warn("proactive refresh failed; serving expiring token", "server", p.server.Name, "err", err)
	return nil
}

func (p *tokenProvider) refreshLocked(stillNeeded func() bool) error {
	if err := p.syncWithSavedLoginLocked(); err != nil {
		return err
	}
	if !stillNeeded() {
		return nil
	}
	token := p.login.Token
	if token.RefreshToken == "" {
		return p.remedyError(fmt.Errorf("no refresh token stored for %s", p.server.Name))
	}
	if token.RefreshToken == p.deadRefreshToken {
		return p.deadRefreshErr
	}
	refreshCtx, cancel := context.WithTimeout(p.lifetime, refreshTimeout)
	defer cancel()
	login := *p.login
	login.Token = withoutAccessToken(token)
	refreshed, err := auth.Refresh(refreshCtx, auth.RefreshParams{Server: p.server, Login: &login})
	if err != nil {
		return p.refreshFailureLocked(err)
	}
	p.login.Token = refreshed
	p.resetRefreshStateLocked()
	p.persistRefreshedLogin()
	return nil
}

// syncWithSavedLoginLocked refuses, rather than refreshes over, a login saved for other settings:
// overwriting it would discard a login the user just made.
func (p *tokenProvider) syncWithSavedLoginLocked() error {
	saved, err := p.loadSavedLogin()
	if err != nil {
		return err
	}
	if !sameLogin(saved, p.persistedLogin) {
		p.adoptLoginLocked(saved)
	}
	return nil
}

func withoutAccessToken(t *oauth2.Token) *oauth2.Token {
	stale := *t
	// oauth2's reuseTokenSource returns any token still valid by the system clock instead of refreshing.
	stale.AccessToken = ""
	return &stale
}

func (p *tokenProvider) refreshFailureLocked(err error) error {
	if refreshNeedsReauth(err) {
		p.deadRefreshToken = p.login.Token.RefreshToken
		p.deadRefreshErr = p.remedyError(fmt.Errorf("refresh token: %w", err))
		return p.deadRefreshErr
	}
	return fmt.Errorf("%s: token refresh failed (transient): %w", p.server.Name, err)
}

func (p *tokenProvider) persistRefreshedLogin() {
	if err := auth.SaveLogin(p.configDir, p.server.Name, p.login); err != nil {
		slog.Warn(
			"persist refreshed oauth login failed; using refreshed login in memory",
			"server",
			p.server.Name,
			"err",
			err,
		)
		return
	}
	p.persistedLogin = cloneLogin(p.login)
}

func (p *tokenProvider) adoptLoginLocked(login *auth.Login) {
	p.login = cloneLogin(login)
	p.persistedLogin = cloneLogin(login)
	p.resetRefreshStateLocked()
}

func sameLogin(a, b *auth.Login) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Issuer == b.Issuer && a.Resource == b.Resource && a.TokenURL == b.TokenURL && a.Client == b.Client &&
		sameToken(a.Token, b.Token)
}

func sameToken(a, b *oauth2.Token) bool {
	return a.AccessToken == b.AccessToken && a.TokenType == b.TokenType &&
		a.RefreshToken == b.RefreshToken && a.Expiry.Equal(b.Expiry) && a.ExpiresIn == b.ExpiresIn
}

func cloneLogin(l *auth.Login) *auth.Login {
	clone := *l
	token := *l.Token
	clone.Token = &token
	return &clone
}

func (p *tokenProvider) resetRefreshStateLocked() {
	p.proactiveRetryAt = time.Time{}
	p.deadRefreshToken = ""
	p.deadRefreshErr = nil
}

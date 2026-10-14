package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
)

const (
	refreshBeforeExpiry     = 5 * time.Minute
	refreshTimeout          = 10 * time.Second
	proactiveRefreshBackoff = 30 * time.Second
)

func (p *tokenProvider) shouldRefreshLocked() bool {
	t := p.login.Token
	if t.Expiry.IsZero() || t.RefreshToken == "" {
		return false
	}
	skew := refreshBeforeExpiry
	const maxBoundedLifetime = int64(refreshBeforeExpiry/time.Second) * 10
	if t.ExpiresIn > 0 && t.ExpiresIn <= maxBoundedLifetime {
		skew = time.Duration(t.ExpiresIn) * time.Second / 10
	}
	return !p.clock.Now().Before(t.Expiry.Add(-skew))
}

func (p *tokenProvider) tokenExpiredLocked() bool {
	expiry := p.login.Token.Expiry
	return !expiry.IsZero() && !p.clock.Now().Before(expiry)
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
	refreshCtx, cancel := context.WithTimeout(p.lifetime, refreshTimeout)
	defer cancel()
	return p.tokenLock().Do(refreshCtx, func(file auth.LoginFile) error {
		login, err := file.Load()
		p.recordDiskLogin(login, err)
		if err != nil {
			return p.loginRemedy("cannot reread login before refresh: " + err.Error())
		}
		if eligible, reason := auth.LoginEligible(login, p.serverConfig()); !eligible {
			return p.loginRemedy(reason)
		}
		p.adoptLoginLocked(login)
		if !stillNeeded() {
			return nil
		}
		return p.exchangeAndPersistLocked(refreshCtx, file)
	})
}

func (p *tokenProvider) exchangeAndPersistLocked(ctx context.Context, file auth.LoginFile) error {
	if p.login.Token.RefreshToken == "" {
		return p.remedyError(fmt.Errorf("no refresh token stored for %s", p.serverName))
	}
	if p.login.Token.RefreshToken == p.deadRefreshToken {
		return p.deadRefreshErr
	}
	refreshed, err := auth.Refresh(ctx, p.login.RefreshConfig(p.configuredAuth), refreshInput(p.login.Token))
	if err != nil {
		return p.refreshFailureLocked(err)
	}
	p.login.Token = refreshed
	p.resetRefreshStateLocked()
	p.persistRefreshedLogin(file)
	return nil
}

func refreshInput(token *oauth2.Token) *oauth2.Token {
	stale := *token
	stale.AccessToken = ""
	return &stale
}

func (p *tokenProvider) refreshFailureLocked(err error) error {
	switch {
	case tokenEndpointGone(err):
		return p.remedyError(fmt.Errorf("token endpoint gone: %w", err))
	case refreshNeedsReauth(err):
		p.deadRefreshToken = p.login.Token.RefreshToken
		p.deadRefreshErr = p.remedyError(fmt.Errorf("refresh token: %w", err))
		return p.deadRefreshErr
	}
	return fmt.Errorf("%s: token refresh failed (transient): %w", p.serverName, err)
}

func (p *tokenProvider) tokenLock() auth.TokenLock {
	return auth.TokenLock{ConfigDir: p.configDir, ServerName: p.serverName, Clock: p.clock}
}

func (p *tokenProvider) resetRefreshStateLocked() {
	p.proactiveRetryAt = time.Time{}
	p.deadRefreshToken = ""
	p.deadRefreshErr = nil
}

func (p *tokenProvider) persistRefreshedLogin(file auth.LoginFile) {
	if err := file.Save(p.login); err != nil {
		slog.Warn(
			"persist refreshed oauth login failed; using refreshed login in memory",
			"server",
			p.serverName,
			"err",
			err,
		)
		return
	}
	p.persistedLogin = cloneLogin(p.login)
	p.observedDiskLogin = cloneLogin(p.login)
	p.observedDiskError = ""
	p.observedLogin = true
}

func (p *tokenProvider) reloadPersistedLoginLocked(load func() (*auth.Login, error)) error {
	login, err := load()
	p.recordDiskLogin(login, err)
	if err != nil {
		return p.loginRemedy("cannot reload login: " + err.Error())
	}
	if eligible, reason := auth.LoginEligible(login, p.serverConfig()); !eligible {
		return p.loginRemedy(reason)
	}
	p.adoptLoginLocked(login)
	return nil
}

func (p *tokenProvider) adoptLoginLocked(login *auth.Login) {
	if samePersistedLogin(login, p.persistedLogin) {
		p.observedDiskLogin = cloneLogin(login)
		p.observedDiskError = ""
		p.diskChanged = false
		p.loginReason = ""
		return
	}
	p.login = cloneLogin(login)
	p.resetRefreshStateLocked()
	p.persistedLogin = cloneLogin(login)
	p.observedLogin = true
	p.observedDiskLogin = cloneLogin(login)
	p.observedDiskError = ""
	p.diskChanged = false
	p.loginReason = ""
}

func samePersistedLogin(a, b *auth.Login) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Resource == b.Resource && a.TokenURL == b.TokenURL && a.Client == b.Client && sameToken(a.Token, b.Token)
}

func sameToken(a, b *oauth2.Token) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AccessToken == b.AccessToken && a.TokenType == b.TokenType && a.RefreshToken == b.RefreshToken &&
		a.Expiry.Equal(b.Expiry) &&
		a.ExpiresIn == b.ExpiresIn
}

func cloneLogin(login *auth.Login) *auth.Login {
	if login == nil {
		return nil
	}
	clone := *login
	clone.Token = cloneToken(login.Token)
	return &clone
}

func cloneToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	clone := *token
	return &clone
}

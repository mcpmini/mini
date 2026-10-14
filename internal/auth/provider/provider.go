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
	configuredAuth *config.AuthConfig
	configDir      string
	serverName     string
	serverURL      string
	unsetEnv       error
	clock          clock.Clock
	lifetime       context.Context

	mu                sync.Mutex
	login             *auth.Login
	persistedLogin    *auth.Login
	observedLogin     bool
	observedDiskLogin *auth.Login
	observedDiskError string
	diskChanged       bool
	loginReason       string
	proactiveRetryAt  time.Time
	deadRefreshToken  string
	deadRefreshErr    error
	retired           bool
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
		if err := p.reloadPersistedLoginLocked(p.loadUnlocked); err != nil {
			return "", err
		}
	}
	if p.shouldRefreshLocked() && !p.inProactiveBackoffLocked() {
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
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if bearerValue(p.login.Token) != stale {
		return bearerValue(p.login.Token), nil
	}
	if err := p.reloadPersistedLoginLocked(p.loadUnlocked); err != nil {
		return "", err
	}
	if bearerValue(p.login.Token) != stale {
		return bearerValue(p.login.Token), nil
	}
	stillStale := func() bool { return bearerValue(p.login.Token) == stale }
	//nolint:contextcheck // The provider mutex serializes rotation; persist it beyond this caller's cancellation.
	if err := p.refreshLocked(stillStale); err != nil {
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

func (p *tokenProvider) ensureTokenLocked() error {
	if p.retired {
		return fmt.Errorf("%s: %w", p.serverName, errRetired)
	}
	if p.login != nil {
		return nil
	}
	login, err := auth.LoadLogin(p.configDir, p.serverName)
	p.recordDiskLogin(login, err)
	if auth.IsNotFound(err) {
		return p.loginRemedy("login file is missing")
	}
	if err != nil {
		return p.loginRemedy(fmt.Errorf("load login: %w", err).Error())
	}
	if eligible, reason := auth.LoginEligible(login, p.serverConfig()); !eligible {
		return p.loginRemedy(reason)
	}
	p.login = login
	p.persistedLogin = cloneLogin(login)
	return nil
}

// loadUnlocked is enough to adopt what another process saved; refreshLocked reads again under the
// token lock before it exchanges anything.
func (p *tokenProvider) loadUnlocked() (*auth.Login, error) {
	return auth.LoadLogin(p.configDir, p.serverName)
}

func (p *tokenProvider) serverConfig() config.ServerConfig {
	return config.ServerConfig{Name: p.serverName, URL: p.serverURL, Auth: p.configuredAuth, UnsetEnv: p.unsetEnv}
}

func (p *tokenProvider) loginRemedy(reason string) error {
	p.loginReason = reason
	return fmt.Errorf("%s; %s: %w", reason, p.loginRemedyText(), transport.NotLoggedInError(p.serverName))
}

func (p *tokenProvider) loginRemedyText() string {
	if p.diskChanged {
		return "configuration changed; restart mini"
	}
	return "run mini auth " + p.serverName
}

func (p *tokenProvider) offlineLoginSnapshot() (auth.LoginSnapshot, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	login, err := p.loadUnlocked()
	p.recordDiskLogin(login, err)
	snapshot := auth.EvaluateLoginAt(
		auth.LoginEvaluation{Login: login, LoadErr: err, Server: p.serverConfig(), Now: p.clock.Now()},
	)
	if snapshot.State == auth.ForOtherSettings || snapshot.State == auth.Missing || snapshot.State == auth.Unreadable {
		p.loginReason = snapshot.Reason
		return snapshot, p.loginRemedyText()
	}
	p.adoptLoginLocked(login)
	if snapshot.State == auth.Expired {
		return snapshot, "run mini auth " + p.serverName
	}
	return snapshot, ""
}

func (p *tokenProvider) recordDiskLogin(login *auth.Login, err error) {
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	changed := p.observedLogin && (errText != p.observedDiskError || !samePersistedLogin(login, p.observedDiskLogin))
	if changed {
		p.diskChanged = true
	}
	p.observedLogin = true
	p.observedDiskError = errText
	p.observedDiskLogin = cloneLogin(login)
}

func (p *tokenProvider) remedyError(cause error) error {
	return transport.ReauthorizationError(p.serverName, cause)
}

func (p *tokenProvider) commitBrowserToken(configured *config.AuthConfig, login *auth.Login) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired {
		return fmt.Errorf("%s: %w", p.serverName, errRetired)
	}
	if err := p.tokenLock().Save(p.lifetime, login); err != nil {
		return fmt.Errorf("persist oauth token: %w", err)
	}
	p.configuredAuth = configured.Clone()
	p.login = cloneLogin(login)
	p.resetRefreshStateLocked()
	p.persistedLogin = cloneLogin(login)
	p.observedLogin = true
	p.observedDiskLogin = cloneLogin(login)
	p.observedDiskError = ""
	p.diskChanged = false
	p.loginReason = ""
	return nil
}

func bearerValue(t *oauth2.Token) string {
	return "Bearer " + t.AccessToken
}

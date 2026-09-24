package auth

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

const refreshSkew = 2 * time.Minute

// refreshTimeout is short because callers queued on a hung token endpoint each wait it out in turn.
const refreshTimeout = 10 * time.Second

const proactiveRefreshBackoff = 30 * time.Second

type ProviderParams struct {
	AuthConfig *config.AuthConfig
	ConfigDir  string
	ServerName string
	ServerURL  string
	Clock      clock.Clock
	Lifetime   context.Context
}

func NewProvider(p ProviderParams) (transport.AuthorizationProvider, error) {
	return buildTokenProvider(p)
}

func buildTokenProvider(p ProviderParams) (*tokenProvider, error) {
	canonical, err := canonicalizeProviderParams(p)
	if err != nil {
		return nil, err
	}
	configured := cloneAuthConfig(canonical.AuthConfig)
	if err := hydrateFromRegistration(canonical); err != nil {
		return nil, err
	}
	tp := newTokenProvider(canonical)
	tp.preHydrationAuthConfig = configured
	return tp, nil
}

func canonicalizeProviderParams(p ProviderParams) (ProviderParams, error) {
	p.AuthConfig = cloneAuthConfig(p.AuthConfig)
	resourceURL := p.AuthConfig.ResourceURL
	if resourceURL == "" {
		resourceURL = p.ServerURL
	}
	if resourceURL != "" {
		canonical, err := canonicalResourceURI(resourceURL)
		if err != nil {
			return ProviderParams{}, err
		}
		p.AuthConfig.ResourceURL = canonical
	}
	return p, nil
}

func normalizeProviderParams(p ProviderParams) (ProviderParams, error) {
	p, err := canonicalizeProviderParams(p)
	if err != nil {
		return ProviderParams{}, err
	}
	if err := hydrateFromRegistration(p); err != nil {
		return ProviderParams{}, err
	}
	return p, nil
}

func newTokenProvider(p ProviderParams) *tokenProvider {
	lifetime := p.Lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	return &tokenProvider{
		ac:         p.AuthConfig,
		configDir:  p.ConfigDir,
		serverName: p.ServerName,
		serverURL:  p.ServerURL,
		clock:      p.Clock,
		lifetime:   lifetime,
	}
}

func cloneAuthConfig(src *config.AuthConfig) *config.AuthConfig {
	if src == nil {
		return nil
	}
	cp := *src
	if src.Scopes != nil {
		cp.Scopes = append([]string{}, src.Scopes...)
	}
	if src.ExtraAuthParams != nil {
		cp.ExtraAuthParams = maps.Clone(src.ExtraAuthParams)
	}
	return &cp
}

func hydrateFromRegistration(p ProviderParams) error {
	if p.AuthConfig.ClientID != "" {
		return nil
	}
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
}

func (p *tokenProvider) Authorization(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureTokenLocked(); err != nil {
		return "", err
	}
	if p.shouldRefreshLocked() {
		p.reloadPersistedTokenLocked()
	}
	if p.shouldRefreshLocked() && !p.inProactiveBackoffLocked() {
		if err := p.proactiveRefreshLocked(); err != nil {
			return "", err
		}
	}
	return bearerValue(p.token), nil
}

func (p *tokenProvider) inProactiveBackoffLocked() bool {
	return !p.tokenExpiredLocked() && p.clock.Now().Before(p.proactiveRetryAt)
}

func (p *tokenProvider) proactiveRefreshLocked() error {
	err := p.refreshLocked()
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

func (p *tokenProvider) tokenExpiredLocked() bool {
	return !p.token.Expiry.IsZero() && !p.clock.Now().Before(p.token.Expiry)
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
	p.reloadPersistedTokenLocked()
	if bearerValue(p.token) != stale {
		return bearerValue(p.token), nil
	}
	if err := p.refreshLocked(); err != nil {
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
	p.persistedToken = cloneToken(t)
	return nil
}

func (p *tokenProvider) reloadPersistedTokenLocked() {
	t, err := Load(p.configDir, p.serverName)
	if err != nil || samePersistedToken(t, p.persistedToken) {
		return
	}
	p.token = t
	p.proactiveRetryAt = time.Time{}
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
	carryOverLazyDiscovery(params.AuthConfig, p.ac)
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

func carryOverLazyDiscovery(rebuilt, current *config.AuthConfig) {
	if rebuilt.TokenURL == "" {
		rebuilt.TokenURL = current.TokenURL
	}
	if rebuilt.AuthURL == "" {
		rebuilt.AuthURL = current.AuthURL
	}
	if rebuilt.ClientID == "" && current.ClientID == ClientMetadataURL {
		rebuilt.ClientID = ClientMetadataURL
	}
}

func (p *tokenProvider) shouldRefreshLocked() bool {
	if p.token.Expiry.IsZero() || p.token.RefreshToken == "" {
		return false
	}
	skew := refreshSkew
	const maxBoundedLifetime = int64(refreshSkew/time.Second) * 10
	if p.token.ExpiresIn > 0 && p.token.ExpiresIn <= maxBoundedLifetime {
		skew = time.Duration(p.token.ExpiresIn) * time.Second / 10
	}
	return !p.clock.Now().Before(p.token.Expiry.Add(-skew))
}

func (p *tokenProvider) refreshLocked() error {
	refreshCtx, cancel := context.WithTimeout(p.lifetime, refreshTimeout)
	defer cancel()
	if err := p.maybeDiscoverAndApplyLocked(refreshCtx); err != nil {
		return err
	}
	// oauth2's reuseTokenSource returns any token still valid by the system clock without refreshing it.
	stale := *p.token
	stale.AccessToken = ""
	refreshed, err := Refresh(refreshCtx, p.ac, &stale)
	if err != nil {
		return p.remedyError(fmt.Errorf("refresh token: %w", err))
	}
	p.token = refreshed
	p.proactiveRetryAt = time.Time{}
	p.persistRefreshedToken(refreshed)
	return nil
}

func (p *tokenProvider) maybeDiscoverAndApplyLocked(ctx context.Context) error {
	if p.ac.TokenURL != "" {
		return nil
	}
	if p.serverURL == "" {
		return p.remedyError(fmt.Errorf("no token endpoint configured and no server URL available for discovery"))
	}
	meta, err := discoverAndApply(ctx, p.serverURL, p.ac)
	if err != nil {
		return p.remedyError(fmt.Errorf("discover token endpoint: %w", err))
	}
	if p.ac.ClientID == "" && meta != nil && meta.CIMDSupported {
		p.ac.ClientID = ClientMetadataURL
	}
	return nil
}

func (p *tokenProvider) persistRefreshedToken(refreshed *oauth2.Token) {
	if err := Save(p.configDir, p.serverName, refreshed); err != nil {
		slog.Warn("persist refreshed oauth token failed; using refreshed token in memory", "server", p.serverName, "err", err)
		return
	}
	p.persistedToken = cloneToken(refreshed)
}

func (p *tokenProvider) remedyError(cause error) error {
	return transport.ReauthorizationError(p.serverName, cause)
}

func (p *tokenProvider) commitBrowserToken(normalized ProviderParams, tok *oauth2.Token) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := Save(p.configDir, p.serverName, tok); err != nil {
		return fmt.Errorf("persist oauth token: %w", err)
	}
	p.ac = normalized.AuthConfig
	p.token = tok
	p.proactiveRetryAt = time.Time{}
	p.persistedToken = cloneToken(tok)
	return nil
}

func bearerValue(t *oauth2.Token) string {
	return "Bearer " + t.AccessToken
}

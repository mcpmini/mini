package auth

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// ProviderCache ensures a single AuthorizationProvider per server within a process.
// All dial paths for a server (startup, reconnect, per-session, runtime-add) share
// one instance so concurrent refreshes collapse to a single token-endpoint hit.
type ProviderCache struct {
	mu sync.Mutex
	m  map[string]*cacheEntry
}

type cacheEntry struct {
	provider *tokenProvider
	identity providerIdentity
}

type providerIdentity struct {
	serverName string
	configDir  string
	serverURL  string
	ac         config.AuthConfig
}

func NewProviderCache() *ProviderCache {
	return &ProviderCache{m: make(map[string]*cacheEntry)}
}

// GetOrCreate returns the cached provider for params.ServerName when its stored
// effective identity matches the incoming params. Returns an error if the same
// server name has an active provider with incompatible parameters.
func (c *ProviderCache) GetOrCreate(params ProviderParams) (transport.AuthorizationProvider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[params.ServerName]; ok {
		if !e.identity.matches(params) {
			return nil, fmt.Errorf("server %q OAuth configuration changed; restart mini to reconfigure", params.ServerName)
		}
		return e.provider, nil
	}
	tp, err := buildTokenProvider(params)
	if err != nil {
		return nil, err
	}
	c.m[params.ServerName] = &cacheEntry{provider: tp, identity: identityFrom(params)}
	return tp, nil
}

// CommitAuthorizedToken installs a browser-authorized token and a freshly hydrated
// OAuth configuration atomically. If no provider is cached yet, it saves the token
// so the next GetOrCreate constructs a hydrated provider from it.
func (c *ProviderCache) CommitAuthorizedToken(params ProviderParams, tok *oauth2.Token) error {
	c.mu.Lock()
	e := c.m[params.ServerName]
	c.mu.Unlock()
	if e == nil {
		return Save(params.ConfigDir, params.ServerName, tok)
	}
	return e.provider.commitBrowserToken(params, tok)
}

func identityFrom(p ProviderParams) providerIdentity {
	ac := config.AuthConfig{}
	if p.AuthConfig != nil {
		ac = *p.AuthConfig
		if p.AuthConfig.Scopes != nil {
			ac.Scopes = append([]string{}, p.AuthConfig.Scopes...)
		}
		if p.AuthConfig.ExtraAuthParams != nil {
			ac.ExtraAuthParams = maps.Clone(p.AuthConfig.ExtraAuthParams)
		}
	}
	return providerIdentity{serverName: p.ServerName, configDir: p.ConfigDir, serverURL: p.ServerURL, ac: ac}
}

func (a providerIdentity) matches(p ProviderParams) bool {
	if a.serverName != p.ServerName || a.configDir != p.ConfigDir || a.serverURL != p.ServerURL {
		return false
	}
	var ac config.AuthConfig
	if p.AuthConfig != nil {
		ac = *p.AuthConfig
	}
	return authConfigEqual(a.ac, ac)
}

func authConfigEqual(a, b config.AuthConfig) bool {
	return a.Type == b.Type && a.Token == b.Token && a.Header == b.Header &&
		a.ClientID == b.ClientID && a.ClientSecret == b.ClientSecret &&
		a.AuthURL == b.AuthURL && a.TokenURL == b.TokenURL &&
		slices.Equal(a.Scopes, b.Scopes) &&
		a.TokenEndpointAuthMethod == b.TokenEndpointAuthMethod &&
		a.ResourceURL == b.ResourceURL && a.CallbackPort == b.CallbackPort &&
		maps.Equal(a.ExtraAuthParams, b.ExtraAuthParams) &&
		a.BrowserCmd == b.BrowserCmd
}

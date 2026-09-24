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

type ProviderRegistry struct {
	mu sync.Mutex
	m  map[string]*registryEntry
}

type registryEntry struct {
	provider *tokenProvider
	identity providerIdentity
}

type providerIdentity struct {
	serverName string
	configDir  string
	serverURL  string
	ac         config.AuthConfig
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{m: make(map[string]*registryEntry)}
}

// GetOrCreate returns the registered provider for params.ServerName when its stored
// effective identity matches the incoming params. Returns an error if the same
// server name has an active provider with incompatible parameters.
func (c *ProviderRegistry) GetOrCreate(params ProviderParams) (transport.AuthorizationProvider, error) {
	normalized, err := normalizeProviderParams(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[normalized.ServerName]; ok {
		if !e.identity.matches(normalized) {
			return nil, fmt.Errorf("server %q OAuth configuration changed; restart mini to reconfigure", normalized.ServerName)
		}
		return e.provider, nil
	}
	tp := newTokenProvider(normalized)
	c.m[normalized.ServerName] = &registryEntry{provider: tp, identity: identityFrom(normalized)}
	return tp, nil
}

// CommitAuthorizedToken installs a browser-authorized token and a freshly hydrated
// OAuth configuration atomically. If no provider is registered yet, it saves the token
// so the next GetOrCreate constructs a hydrated provider from it.
func (c *ProviderRegistry) CommitAuthorizedToken(params ProviderParams, tok *oauth2.Token) error {
	normalized, err := normalizeProviderParams(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[normalized.ServerName]
	if e == nil {
		return Save(normalized.ConfigDir, normalized.ServerName, tok)
	}
	if e.identity.serverName != normalized.ServerName ||
		e.identity.configDir != normalized.ConfigDir ||
		e.identity.serverURL != normalized.ServerURL {
		return fmt.Errorf("server %q OAuth identity changed; refusing trusted commit", normalized.ServerName)
	}
	if err := e.provider.commitBrowserToken(normalized, tok); err != nil {
		return err
	}
	e.identity = identityFrom(normalized)
	return nil
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

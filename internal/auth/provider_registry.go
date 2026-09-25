package auth

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/transport"
)

type ProviderRegistry struct {
	mu     sync.Mutex
	m      map[string]*registryEntry
	ctx    context.Context
	cancel context.CancelFunc
}

type registryEntry struct {
	provider *tokenProvider
	identity providerIdentity
}

type providerIdentity struct {
	serverName string
	configDir  string
	serverURL  string
}

func NewProviderRegistry() *ProviderRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProviderRegistry{m: make(map[string]*registryEntry), ctx: ctx, cancel: cancel}
}

func (c *ProviderRegistry) Close() {
	c.cancel()
}

// GetOrCreate returns the provider registered for params.ServerName, creating it on first use.
// It errors if ConfigDir or ServerURL changed; AuthConfig drift is ignored because the provider owns it.
func (c *ProviderRegistry) GetOrCreate(params ProviderParams) (transport.AuthorizationProvider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[params.ServerName]; ok {
		if !e.identity.matches(params) {
			return nil, fmt.Errorf("server %q OAuth configuration changed; restart mini to reconfigure", params.ServerName)
		}
		return e.provider, nil
	}
	params.Lifetime = c.ctx
	tp, err := buildTokenProvider(params)
	if err != nil {
		return nil, err
	}
	c.m[params.ServerName] = &registryEntry{provider: tp, identity: identityFrom(params)}
	return tp, nil
}

// CommitAuthorizedToken persists tok and installs it, with params' hydrated config, into the
// server's provider if one exists. It refuses a provider whose server URL or config dir differs.
func (c *ProviderRegistry) CommitAuthorizedToken(params ProviderParams, tok *oauth2.Token) error {
	configured, hydrated, err := resolveAuthConfigs(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	e := c.m[params.ServerName]
	if e != nil && !e.identity.matches(params) {
		c.mu.Unlock()
		return fmt.Errorf("server %q OAuth identity changed; refusing trusted commit", params.ServerName)
	}
	c.mu.Unlock()
	if e == nil {
		return Save(params.ConfigDir, params.ServerName, tok)
	}
	return e.provider.commitBrowserToken(hydrated, configured, tok)
}

func identityFrom(p ProviderParams) providerIdentity {
	return providerIdentity{serverName: p.ServerName, configDir: p.ConfigDir, serverURL: p.ServerURL}
}

func (a providerIdentity) matches(p ProviderParams) bool {
	return a.serverName == p.ServerName && a.configDir == p.ConfigDir && a.serverURL == p.ServerURL
}

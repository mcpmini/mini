package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type Registry struct {
	mu     sync.Mutex
	m      map[string]*registryEntry
	ctx    context.Context
	cancel context.CancelFunc
}

type registryEntry struct {
	provider *tokenProvider
	identity providerIdentity
	cancel   context.CancelFunc
}

type providerIdentity struct {
	serverName string
	configDir  string
	serverURL  string
}

func NewRegistry() *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{m: make(map[string]*registryEntry), ctx: ctx, cancel: cancel}
}

func (c *Registry) Close() {
	c.cancel()
}

func (c *Registry) OfflineLoginSnapshot(serverName string) (auth.LoginSnapshot, string, bool) {
	c.mu.Lock()
	e := c.m[serverName]
	c.mu.Unlock()
	if e == nil {
		return auth.LoginSnapshot{}, "", false
	}
	snapshot, remedy := e.provider.offlineLoginSnapshot()
	return snapshot, remedy, true
}

// GetOrCreate returns the provider registered for params.ServerName, creating it on first use.
// It errors if ConfigDir or ServerURL changed; AuthConfig drift is ignored because the provider owns it.
func (c *Registry) GetOrCreate(params Params) (transport.AuthorizationProvider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[params.ServerName]; ok {
		if !e.identity.matches(params) {
			return nil, fmt.Errorf(
				"server %q OAuth configuration changed; restart mini to reconfigure",
				params.ServerName,
			)
		}
		return e.provider, nil
	}
	lifetime, cancel := context.WithCancel(c.ctx)
	params.Lifetime = lifetime
	tp, err := buildTokenProvider(params)
	if err != nil {
		cancel()
		return nil, err
	}
	c.m[params.ServerName] = &registryEntry{provider: tp, identity: identityFrom(params), cancel: cancel}
	return tp, nil
}

// Forget drops the server's provider, so the next GetOrCreate for the name starts from disk.
// It returns once the old provider's in-flight refresh or commit has finished, and that
// provider never authorizes or saves a token again.
func (c *Registry) Forget(serverName string) {
	c.mu.Lock()
	e := c.m[serverName]
	delete(c.m, serverName)
	c.mu.Unlock()
	if e == nil {
		return
	}
	e.cancel()
	e.provider.retire()
}

// CommitAuthorizedToken saves tok and, if the server already has a provider, installs it there.
func (c *Registry) CommitAuthorizedToken(params Params, login *auth.Login) error {
	serverConfig := config.ServerConfig{
		Name:     params.ServerName,
		URL:      params.ServerURL,
		Auth:     params.AuthConfig,
		UnsetEnv: params.UnsetEnv,
	}
	if eligible, reason := auth.LoginEligible(login, serverConfig); !eligible {
		return fmt.Errorf("refusing oauth login for %s: %s", params.ServerName, reason)
	}
	c.mu.Lock()
	e := c.m[params.ServerName]
	if e != nil && !e.identity.matches(params) {
		c.mu.Unlock()
		return fmt.Errorf("server %q OAuth identity changed; refusing trusted commit", params.ServerName)
	}
	c.mu.Unlock()
	if e == nil {
		clk := params.Clock
		if clk == nil {
			clk = clock.System()
		}
		lock := auth.TokenLock{ConfigDir: params.ConfigDir, ServerName: params.ServerName, Clock: clk}
		return lock.Save(c.ctx, login)
	}
	return e.provider.commitBrowserToken(params.AuthConfig, login)
}

func identityFrom(p Params) providerIdentity {
	return providerIdentity{serverName: p.ServerName, configDir: p.ConfigDir, serverURL: p.ServerURL}
}

func (a providerIdentity) matches(p Params) bool {
	return a.serverName == p.ServerName && a.configDir == p.ConfigDir && a.serverURL == p.ServerURL
}

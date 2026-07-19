package auth

import (
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

// ProviderCache ensures a single AuthorizationProvider per server within a process.
// All dial paths for a server (startup, reconnect, per-session, runtime-add) share
// one instance so concurrent refreshes collapse to a single token-endpoint hit.
type ProviderCache struct {
	mu sync.Mutex
	m  map[string]transport.AuthorizationProvider
}

func NewProviderCache() *ProviderCache {
	return &ProviderCache{m: make(map[string]transport.AuthorizationProvider)}
}

// GetOrCreate returns the cached provider for params.ServerName, or builds and
// caches a new one. The provider is already mutex-guarded for concurrent use.
func (c *ProviderCache) GetOrCreate(params ProviderParams) (transport.AuthorizationProvider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.m[params.ServerName]; ok {
		return p, nil
	}
	p, err := NewProvider(params)
	if err != nil {
		return nil, err
	}
	c.m[params.ServerName] = p
	return p, nil
}

// Evict removes the cached provider for serverName. Call before re-dialing with
// a changed config so the next GetOrCreate constructs a fresh provider.
func (c *ProviderCache) Evict(serverName string) {
	c.mu.Lock()
	delete(c.m, serverName)
	c.mu.Unlock()
}

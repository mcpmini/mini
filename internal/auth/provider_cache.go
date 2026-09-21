package auth

import (
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

type ProviderCache struct {
	mu sync.Mutex
	m  map[string]transport.AuthorizationProvider
}

func NewProviderCache() *ProviderCache {
	return &ProviderCache{m: make(map[string]transport.AuthorizationProvider)}
}

func (c *ProviderCache) GetOrCreate(params ProviderParams) (transport.AuthorizationProvider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if provider, ok := c.m[params.ServerName]; ok {
		return provider, nil
	}
	provider, err := NewProvider(params)
	if err != nil {
		return nil, err
	}
	c.m[params.ServerName] = provider
	return provider, nil
}

func (c *ProviderCache) Evict(serverName string) {
	c.mu.Lock()
	delete(c.m, serverName)
	c.mu.Unlock()
}

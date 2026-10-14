package main

import (
	"context"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

type cliConnection struct {
	transport.Connection
	registry *provider.Registry
}

func dialCLI(ctx context.Context, p invoke.DialParams) (transport.Connection, error) {
	registry := provider.NewRegistry()
	p.ProviderRegistry = registry
	conn, err := invoke.Dial(ctx, p)
	if err != nil {
		registry.Close()
		return nil, err
	}
	return &cliConnection{Connection: conn, registry: registry}, nil
}

func (c *cliConnection) Close() error {
	c.registry.Close()
	return c.Connection.Close()
}

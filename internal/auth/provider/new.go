package provider

import (
	"context"
	"errors"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type Params struct {
	AgentAdded bool
	AuthConfig *config.AuthConfig
	ConfigDir  string
	ServerName string
	ServerURL  string
	Clock      clock.Clock
	Lifetime   context.Context
}

func buildTokenProvider(p Params) (*tokenProvider, error) {
	if p.AuthConfig == nil {
		return nil, errors.New("AuthConfig is required")
	}
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	lifetime := p.Lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	return &tokenProvider{
		server:    configuredServer(p),
		configDir: p.ConfigDir,
		clock:     p.Clock,
		lifetime:  lifetime,
	}, nil
}

func configuredServer(p Params) config.ServerConfig {
	return config.ServerConfig{
		Name:       p.ServerName,
		URL:        p.ServerURL,
		Auth:       p.AuthConfig.Clone(),
		AgentAdded: p.AgentAdded,
	}
}

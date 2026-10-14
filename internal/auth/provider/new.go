package provider

import (
	"context"
	"errors"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type Params struct {
	AuthConfig *config.AuthConfig
	ConfigDir  string
	ServerName string
	ServerURL  string
	UnsetEnv   error
	Clock      clock.Clock
	Lifetime   context.Context
}

func buildTokenProvider(p Params) (*tokenProvider, error) {
	configured, err := canonicalizeParams(p)
	if err != nil {
		return nil, err
	}
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	p = configured
	return newTokenProvider(p, configured.AuthConfig), nil
}

func canonicalizeParams(p Params) (Params, error) {
	if p.AuthConfig == nil {
		return Params{}, errors.New("AuthConfig is required")
	}
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	p.AuthConfig = p.AuthConfig.Clone()
	if p.ServerURL != "" {
		if _, err := auth.CanonicalResourceURI(p.ServerURL); err != nil {
			return Params{}, err
		}
	}
	return p, nil
}

func newTokenProvider(p Params, configured *config.AuthConfig) *tokenProvider {
	lifetime := p.Lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	return &tokenProvider{
		configuredAuth: configured,
		configDir:      p.ConfigDir,
		serverName:     p.ServerName,
		serverURL:      p.ServerURL,
		unsetEnv:       p.UnsetEnv,
		clock:          p.Clock,
		lifetime:       lifetime,
	}
}

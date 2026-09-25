package auth

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type ProviderParams struct {
	AuthConfig *config.AuthConfig
	ConfigDir  string
	ServerName string
	ServerURL  string
	Clock      clock.Clock
	Lifetime   context.Context
}

func buildTokenProvider(p ProviderParams) (*tokenProvider, error) {
	canonical, err := canonicalizeProviderParams(p)
	if err != nil {
		return nil, err
	}
	preHydration := cloneAuthConfig(canonical.AuthConfig)
	if err := hydrateFromRegistration(canonical); err != nil {
		return nil, err
	}
	tp := newTokenProvider(canonical)
	tp.preHydrationAuthConfig = preHydration
	return tp, nil
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

func canonicalizeProviderParams(p ProviderParams) (ProviderParams, error) {
	if p.AuthConfig == nil {
		return ProviderParams{}, errors.New("AuthConfig is required")
	}
	if p.Clock == nil {
		p.Clock = clock.System()
	}
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

func newTokenProvider(p ProviderParams) *tokenProvider {
	lifetime := p.Lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	return &tokenProvider{
		ac:         p.AuthConfig,
		configDir:  p.ConfigDir,
		serverName: p.ServerName,
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
	_, err := applyExistingClientReg(clientRegParams{
		ConfigDir:  p.ConfigDir,
		ServerName: p.ServerName,
		AuthConfig: p.AuthConfig,
		Now:        p.Clock.Now(),
	})
	if err != nil {
		return fmt.Errorf("load client registration for %s: %w", p.ServerName, err)
	}
	return nil
}

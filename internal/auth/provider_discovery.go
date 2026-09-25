package auth

import (
	"context"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

func (p *tokenProvider) maybeDiscoverAndApplyLocked(ctx context.Context) error {
	if p.ac.TokenURL != "" {
		return nil
	}
	if p.serverURL == "" {
		return p.remedyError(fmt.Errorf("no token endpoint configured and no server URL available for discovery"))
	}
	meta, err := discoverAndApply(ctx, p.serverURL, p.ac)
	if err != nil {
		return p.remedyError(fmt.Errorf("discover token endpoint: %w", err))
	}
	if p.ac.ClientID == "" && meta != nil && meta.CIMDSupported {
		p.ac.ClientID = ClientMetadataURL
	}
	return nil
}

func carryOverLazyDiscovery(rebuilt, current *config.AuthConfig) {
	if rebuilt.TokenURL == "" {
		rebuilt.TokenURL = current.TokenURL
	}
	if rebuilt.AuthURL == "" {
		rebuilt.AuthURL = current.AuthURL
	}
	// CIMD client ID is stable and server-issued, so it survives token adoption.
	// DCR client IDs are not: the external token may come from a different registration.
	if rebuilt.ClientID == "" && current.ClientID == ClientMetadataURL {
		rebuilt.ClientID = ClientMetadataURL
	}
}

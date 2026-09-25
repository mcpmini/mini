package auth

import (
	"context"
	"fmt"
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

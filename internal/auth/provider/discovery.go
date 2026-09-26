package provider

import (
	"context"
	"fmt"

	"github.com/mcpmini/mini/internal/auth"
)

func (p *tokenProvider) maybeDiscoverAndApplyLocked(ctx context.Context) error {
	if p.ac.TokenURL != "" {
		return nil
	}
	if p.serverURL == "" {
		return p.remedyError(fmt.Errorf("no token endpoint configured and no server URL available for discovery"))
	}
	meta, err := auth.DiscoverAndApply(ctx, p.serverURL, p.ac)
	if err != nil {
		return fmt.Errorf("%s: token endpoint discovery failed (transient): %w", p.serverName, err)
	}
	if p.ac.ClientID == "" && meta != nil && meta.CIMDSupported {
		p.ac.ClientID = auth.ClientMetadataURL
	}
	return nil
}

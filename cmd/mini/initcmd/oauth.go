package initcmd

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

const oauthCheckTimeout = 5 * time.Second

type probeParams struct {
	configDir string
	server    config.ServerConfig
	clock     clock.Clock
}

type probeFunc func(ctx context.Context, configDir string, sc config.ServerConfig) error

// The probe saves an OAuth finding itself; its error tells a refusal without OAuth from the rest.
// A probe cut short may have seen the 401 but not yet the OAuth metadata, so its error proves nothing.
func checkOAuth(ctx context.Context, p probeParams, probe probeFunc) error {
	ctx, cancel := clock.WithTimeout(ctx, p.clock, oauthCheckTimeout)
	defer cancel()
	err := probe(ctx, p.configDir, p.server)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

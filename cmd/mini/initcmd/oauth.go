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
func checkOAuth(ctx context.Context, p probeParams, probe probeFunc) error {
	ctx, cancel := clock.WithTimeout(ctx, p.clock, oauthCheckTimeout)
	defer cancel()
	return probe(ctx, p.configDir, p.server)
}

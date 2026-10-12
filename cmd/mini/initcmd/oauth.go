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

func checkOAuth(ctx context.Context, p probeParams, probe probeFunc) {
	ctx, cancel := clock.WithTimeout(ctx, p.clock, oauthCheckTimeout)
	defer cancel()
	//nolint:errcheck // Unreachable servers are left for the proxy; OAuth checks consume auth configuration saved by the probe.
	probe(ctx, p.configDir, p.server)
}

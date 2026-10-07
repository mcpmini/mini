package initcmd

import (
	"context"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

const oauthCheckTimeout = 5 * time.Second

// The login step lists an OAuth server only once a probe records that it needs OAuth.
func oauthTargets(configDir string, names []string) []config.ServerConfig {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil // a later init step hits the same error and reports it
	}
	var targets []config.ServerConfig
	for _, sc := range servers.Loaded {
		if slices.Contains(names, sc.Name) && ops.MayNeedOAuth(sc) {
			targets = append(targets, sc)
		}
	}
	return targets
}

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

package initcmd

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/server"
)

const OAuthCheckTimeout = 5 * time.Second

// OAuthTargets are the named servers a probe could prove need OAuth; the login step lists an
// OAuth server only once that is recorded.
func OAuthTargets(configDir string, names []string) []config.ServerConfig {
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

// CheckOAuth blocks until every probe finishes or times out.
func CheckOAuth(configDir string, servers []config.ServerConfig, clk clock.Clock) {
	var wg sync.WaitGroup
	for _, sc := range servers {
		wg.Go(func() { checkOAuth(configDir, sc, clk) })
	}
	wg.Wait()
}

func checkOAuth(configDir string, sc config.ServerConfig, clk clock.Clock) {
	ctx, cancel := clock.WithTimeout(context.Background(), clk, OAuthCheckTimeout)
	defer cancel()
	// Only the OAuth requirement the probe records matters; an unreachable server is left for the proxy.
	server.ProbeServer(ctx, configDir, sc) //nolint:errcheck
}

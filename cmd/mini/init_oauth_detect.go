package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

const oauthProbeTimeout = 5 * time.Second

type oauthDetectParams struct {
	configDir string
	names     []string
	clock     clock.Clock
	errOut    io.Writer
}

// detectImportedOAuth probes only the servers this run imported: the login step
// can't list an OAuth server until something has recorded that it needs OAuth.
func detectImportedOAuth(p oauthDetectParams) {
	servers, err := config.LoadServers(p.configDir)
	if err != nil {
		return // the catalog and login steps that run next hit the same error and report it
	}
	targets := oauthDetectionTargets(servers.Loaded, p.names)
	if len(targets) == 0 {
		return
	}
	fmt.Fprintf(p.errOut, "checking %d imported server(s) for OAuth...\n", len(targets))
	var wg sync.WaitGroup
	for _, sc := range targets {
		wg.Go(func() { p.detectOne(sc) })
	}
	wg.Wait()
}

func oauthDetectionTargets(servers []config.ServerConfig, names []string) []config.ServerConfig {
	var targets []config.ServerConfig
	for _, sc := range servers {
		if slices.Contains(names, sc.Name) && authUndiscovered(sc) {
			targets = append(targets, sc)
		}
	}
	return targets
}

func (p oauthDetectParams) detectOne(sc config.ServerConfig) {
	ctx, cancel := p.probeContext()
	defer cancel()
	// Only the recorded OAuth requirement matters here; an unreachable server is left for the proxy.
	probeConnection(ctx, p.configDir, sc) //nolint:errcheck
}

// The deadline runs on p.clock so tests can expire it without waiting it out.
func (p oauthDetectParams) probeContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	deadline := p.clock.NewTimer(oauthProbeTimeout)
	go func() {
		select {
		case <-deadline.Chan():
			cancel()
		case <-ctx.Done():
			deadline.Stop()
		}
	}()
	return ctx, cancel
}

package initcmd

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

const oauthCheckTimeout = 5 * time.Second

func (s *Session) startChecks() {
	targets := s.checkTargets()
	ctx, cancel := context.WithCancel(context.Background())
	s.checks.cancel = cancel
	s.mu.Lock()
	for _, sc := range targets {
		s.checking[sc.Name] = true
	}
	s.mu.Unlock()
	for _, sc := range targets {
		s.checks.wg.Go(func() { s.check(ctx, sc) })
	}
	// stopChecks showed every check finished; the restarted ones are running again.
	s.notifyChanged()
}

// Checks the servers as loaded, so bundled and already-detected auth count.
func (s *Session) checkTargets() []config.ServerConfig {
	servers, err := config.LoadServers(s.p.ConfigDir)
	if err != nil {
		// Unchecked servers are left as they are: the proxy detects OAuth on first connect.
		return nil
	}
	var targets []config.ServerConfig
	for _, sc := range servers.Loaded {
		if _, ok := s.written[sc.Name]; ok && !s.checked[sc.Name] && ops.MayNeedOAuth(sc) {
			targets = append(targets, sc)
		}
	}
	return targets
}

func (s *Session) check(ctx context.Context, sc config.ServerConfig) {
	probeCtx, cancel := clock.WithTimeout(ctx, s.p.Clock, oauthCheckTimeout)
	defer cancel()
	// Only the OAuth requirement the probe records matters; an unreachable server is left for the proxy.
	s.p.Probe(probeCtx, s.p.ConfigDir, sc) //nolint:errcheck
	s.mu.Lock()
	delete(s.checking, sc.Name)
	// A cancelled check proved nothing, so the next sync runs it again; a timed-out one is done.
	if ctx.Err() == nil {
		s.checked[sc.Name] = true
	}
	s.mu.Unlock()
	s.notifyChanged()
}

func (s *Session) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *Session) stopChecks() {
	if s.checks.cancel != nil {
		s.checks.cancel()
	}
	s.checks.wg.Wait()
	s.mu.Lock()
	clear(s.checking)
	s.mu.Unlock()
}

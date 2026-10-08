package initcmd

import (
	"context"

	"github.com/mcpmini/mini/internal/config"
)

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
	// stopChecks cleared every check, so watchers must hear these run again.
	s.notifyChanged()
}

func (s *Session) checkTargets() []config.ServerConfig {
	var unchecked []string
	s.mu.Lock()
	for name := range s.written {
		if !s.checked[name] {
			unchecked = append(unchecked, name)
		}
	}
	s.mu.Unlock()
	// Read back from disk, so bundled and already-detected auth count.
	return OAuthTargets(s.p.ConfigDir, unchecked)
}

func (s *Session) check(ctx context.Context, sc config.ServerConfig) {
	checkOAuth(ctx, probeParams{configDir: s.p.ConfigDir, server: sc, clock: s.p.Clock}, s.p.Probe)
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

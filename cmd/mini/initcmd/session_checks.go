package initcmd

import (
	"context"
	"errors"
	"slices"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/transport"
)

type checkTarget struct {
	configDir string
	server    config.ServerConfig
	// Only a server this run wrote whose auth is unknown is probed: the probe records its OAuth
	// finding in its config dir, and servers the user had before are never touched.
	probe bool
}

func (s *session) startChecks() {
	targets := s.checkTargets()
	ctx, cancel := context.WithCancel(context.Background())
	s.checks.cancel = cancel
	s.mu.Lock()
	for _, t := range targets {
		s.checking[t.server.Name] = true
	}
	s.mu.Unlock()
	for _, t := range targets {
		s.checks.wg.Go(func() { s.check(ctx, t) })
	}
	// stopChecks cleared every check, so watchers must hear these run again.
	s.notifyChanged()
}

func (s *session) checkTargets() []checkTarget {
	var targets []checkTarget
	// Read back from disk, so bundled and already-detected auth count.
	for _, sc := range s.unchecked(s.p.ConfigDir) {
		_, written := s.written[sc.Name]
		if written && (ops.MayNeedOAuth(sc) || needsRegistrationCheck(s.p.ConfigDir, sc)) {
			targets = append(targets, checkTarget{configDir: s.p.ConfigDir, server: sc, probe: ops.MayNeedOAuth(sc)})
		}
	}
	for _, sc := range s.unchecked(s.p.MiniDir) {
		if needsRegistrationCheck(s.p.MiniDir, sc) {
			targets = append(targets, checkTarget{configDir: s.p.MiniDir, server: sc})
		}
	}
	return targets
}

func (s *session) unchecked(configDir string) []config.ServerConfig {
	if configDir == "" {
		return nil
	}
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil // a later init step hits the same error and reports it
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.DeleteFunc(servers.Loaded, func(sc config.ServerConfig) bool { return s.checked[sc.Name] })
}

func (s *session) check(ctx context.Context, t checkTarget) {
	var probeErr error
	if t.probe {
		probeErr = checkOAuth(ctx, probeParams{configDir: t.configDir, server: t.server, clock: s.p.Clock}, s.p.Probe)
	}
	s.recordFindings(ctx, t.configDir, t.server.Name, probeErr)
	s.mu.Lock()
	delete(s.checking, t.server.Name)
	// A cancelled check proved nothing, so the next sync runs it again; a timed-out one is done.
	if ctx.Err() == nil {
		s.checked[t.server.Name] = true
	}
	s.mu.Unlock()
	s.notifyChanged()
}

// Reads the server again after the probe, which may have found that it needs an OAuth login. One
// that refused the probe without an OAuth challenge needs a credential the user must supply.
func (s *session) recordFindings(ctx context.Context, configDir, name string, probeErr error) {
	sc, err := config.LoadServer(configDir, name)
	if err != nil {
		return
	}
	var unauthorized *transport.UnauthorizedError
	if errors.As(probeErr, &unauthorized) && !sc.UsesOAuthLogin() {
		s.mu.Lock()
		s.findings.NeedsCredential[name] = true
		s.mu.Unlock()
		return
	}
	if needsRegistrationCheck(configDir, sc) {
		s.checkRegistration(ctx, sc)
	}
}

func (s *session) checkRegistration(ctx context.Context, sc config.ServerConfig) {
	name := sc.Name
	check := registrationCheck{server: sc, clock: s.p.Clock, discover: s.p.Discover}
	selfRegisters, known := check.selfRegisters(ctx)
	if !known {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if selfRegisters {
		delete(s.findings.NoSelfRegistration, name)
	} else {
		s.findings.NoSelfRegistration[name] = true
	}
}

func (s *session) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *session) stopChecks() {
	if s.checks.cancel != nil {
		s.checks.cancel()
	}
	s.checks.wg.Wait()
	s.mu.Lock()
	clear(s.checking)
	s.mu.Unlock()
}

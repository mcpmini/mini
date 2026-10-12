package initcmd

import (
	"context"
	"slices"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type checkTarget struct {
	configDir string
	server    config.ServerConfig
	// Only a server this run wrote is probed: the probe records its OAuth finding in its config dir,
	// and servers the user had before are never touched.
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
		if _, written := s.written[sc.Name]; written &&
			(ops.MayNeedOAuth(sc) || needsRegistrationCheck(s.p.ConfigDir, sc)) {
			targets = append(targets, checkTarget{configDir: s.p.ConfigDir, server: sc, probe: true})
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
	if t.probe {
		checkOAuth(ctx, probeParams{configDir: t.configDir, server: t.server, clock: s.p.Clock}, s.p.Probe)
	}
	s.checkRegistration(ctx, t.configDir, t.server.Name)
	s.mu.Lock()
	delete(s.checking, t.server.Name)
	// A cancelled check proved nothing, so the next sync runs it again; a timed-out one is done.
	if ctx.Err() == nil {
		s.checked[t.server.Name] = true
	}
	s.mu.Unlock()
	s.notifyChanged()
}

// Reads the server again after the probe, which may have found that it needs an OAuth login.
func (s *session) checkRegistration(ctx context.Context, configDir, name string) {
	sc, ok := loadServer(configDir, name)
	if !ok || !needsRegistrationCheck(configDir, sc) {
		return
	}
	check := registrationCheck{server: sc, clock: s.p.Clock, discover: s.p.Discover}
	selfRegisters, known := check.selfRegisters(ctx)
	if !known {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if selfRegisters {
		delete(s.noSelfRegistration, name)
	} else {
		s.noSelfRegistration[name] = true
	}
}

func loadServer(configDir, name string) (config.ServerConfig, bool) {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return config.ServerConfig{}, false
	}
	i := slices.IndexFunc(servers.Loaded, func(sc config.ServerConfig) bool { return sc.Name == name })
	if i < 0 {
		return config.ServerConfig{}, false
	}
	return servers.Loaded[i], true
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

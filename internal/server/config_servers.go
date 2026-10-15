package server

import (
	"context"
	"slices"

	"github.com/mcpmini/mini/internal/config"
)

func (s *Server) recordConfigServers(servers []config.ServerConfig) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for _, sc := range servers {
		if sc.IsEnabled() {
			s.configServers[sc.Name] = true
		}
	}
}

func (s *Server) configServerNames() []string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	names := make([]string, 0, len(s.configServers))
	for name := range s.configServers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// applyServerFiles connects and removes servers to match servers/. It looks at every name each tick,
// so a name skipped while busy or broken is picked up once it isn't.
func (s *Server) applyServerFiles(ctx context.Context) {
	names, err := s.namesToApply()
	if err != nil {
		return // a listing error must never remove servers; the next tick tries again
	}
	removed := false
	for _, name := range names {
		removed = s.applyServerFile(ctx, name) || removed
	}
	if removed {
		s.notifyAllSessions()
	}
}

func (s *Server) namesToApply() ([]string, error) {
	saved, err := config.ServerNames(s.configDir)
	if err != nil {
		return nil, err
	}
	names := append(s.configServerNames(), saved...)
	slices.Sort(names)
	return slices.Compact(names), nil
}

func (s *Server) applyServerFile(ctx context.Context, name string) (removed bool) {
	unlock, ok := s.serverNames.tryLock(name)
	if !ok {
		return false
	}
	defer unlock()
	sc, found, err := config.LookupServer(s.configDir, name)
	if err != nil {
		return false
	}
	wanted := found && sc.IsEnabled()
	switch {
	case !wanted && s.isConfigServer(name):
		s.detachAndCloseServer(name)
		s.logger.Info("server removed by config change", "server", name)
		return true
	// An empty or half-written file decodes without error.
	case wanted && !s.isKnownServer(name) && (sc.Command != "" || sc.URL != ""):
		s.logger.Info("server added by config change", "server", name)
		s.ConnectUpstreams(ctx, []config.ServerConfig{sc})
	}
	return false
}

func (s *Server) isKnownServer(name string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.upstreams[name] != nil || s.configServers[name]
}

func (s *Server) isConfigServer(name string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.configServers[name]
}

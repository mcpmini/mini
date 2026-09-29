package server

import (
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

func (s *Server) removeServersGoneFromConfig() {
	set := config.LoadServerSet(s.configDir)
	if len(set.SourceErrors) > 0 {
		s.logger.Warn("config reload: not removing servers while a config file fails to load", "files", sourceErrorPaths(set.SourceErrors))
		return
	}
	removed := false
	for _, name := range s.configServerNames() {
		if !set.Wants(name) {
			s.detachAndCloseServer(name)
			s.logger.Info("server removed by config change", "server", name)
			removed = true
		}
	}
	if removed {
		s.notifyAllSessions()
	}
}

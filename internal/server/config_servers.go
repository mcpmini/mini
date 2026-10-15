package server

import (
	"context"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type configServer struct {
	// loginSavedAt is the token file's time when the server stopped at needs auth. Taken then, not
	// before connecting, so a token mini refreshed during that attempt doesn't count as a new login.
	loginSavedAt time.Time
}

func (s *Server) recordConfigServers(servers []config.ServerConfig) {
	records := make(map[string]configServer, len(servers))
	for _, sc := range servers {
		if sc.IsEnabled() {
			records[sc.Name] = configServer{}
		}
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	maps.Copy(s.configServers, records)
}

func (s *Server) loginSavedAt(name string) time.Time {
	info, err := os.Stat(auth.TokenPath(s.configDir, name))
	if err != nil {
		return time.Time{} // no saved login yet
	}
	return info.ModTime()
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
	if !found || !sc.IsEnabled() {
		return s.removeConfigServer(name)
	}
	// An empty or half-written file decodes without error.
	if sc.Command == "" && sc.URL == "" {
		return false
	}
	s.connectConfigServer(ctx, sc)
	return false
}

func (s *Server) removeConfigServer(name string) bool {
	if !s.isConfigServer(name) {
		return false
	}
	s.detachAndCloseServer(name)
	s.logger.Info("server removed by config change", "server", name)
	return true
}

func (s *Server) connectConfigServer(ctx context.Context, sc config.ServerConfig) {
	switch {
	case !s.isKnownServer(sc.Name):
		s.logger.Info("server added by config change", "server", sc.Name)
		s.ConnectUpstreams(ctx, []config.ServerConfig{sc})
	case s.loginSavedSinceAuthFailed(sc.Name):
		s.logger.Info("reconnecting server with its saved login", "server", sc.Name)
		s.reconnectConfigServer(ctx, sc)
	}
}

func (s *Server) loginSavedSinceAuthFailed(name string) bool {
	if s.hasAuthFlow(name) { // a login start_auth runs reconnects the server itself
		return false
	}
	s.stateMu.RLock()
	record, recorded := s.configServers[name]
	_, needsAuth := s.startup.state(name, s.clock.Now()).status.(needsAuthStatus)
	s.stateMu.RUnlock()
	return recorded && needsAuth && s.loginSavedAt(name).After(record.loginSavedAt)
}

func (s *Server) reconnectConfigServer(ctx context.Context, sc config.ServerConfig) {
	s.serverOpMu.Lock()
	s.removeGen[sc.Name]++
	s.closeDetached(sc.Name, s.detachUpstreamKeepingProjections(sc.Name))
	s.serverOpMu.Unlock()
	s.ConnectUpstreams(ctx, []config.ServerConfig{sc})
}

func (s *Server) isKnownServer(name string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	_, recorded := s.configServers[name]
	return s.upstreams[name] != nil || recorded
}

func (s *Server) isConfigServer(name string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	_, recorded := s.configServers[name]
	return recorded
}

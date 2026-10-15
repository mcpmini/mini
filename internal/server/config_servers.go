package server

import (
	"context"
	"maps"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type configServer struct {
	settings config.ServerConfig
	// loginSavedAt is the token file's time when the server stopped at needs auth. Taken then, not
	// before connecting, so a token mini refreshed during that attempt doesn't count as a new login.
	loginSavedAt time.Time
}

func (s *Server) recordConfigServers(servers []config.ServerConfig) {
	records := make(map[string]configServer, len(servers))
	for _, sc := range servers {
		if sc.IsEnabled() {
			records[sc.Name] = configServer{settings: withOwnAuth(sc)}
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
	toolsDropped := false
	for _, name := range names {
		toolsDropped = s.applyServerFile(ctx, name) || toolsDropped
	}
	// A server that connects notifies agents itself; one whose new settings fail never does.
	if toolsDropped {
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

func (s *Server) applyServerFile(ctx context.Context, name string) (toolsDropped bool) {
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
	return s.connectConfigServer(ctx, sc)
}

func (s *Server) removeConfigServer(name string) bool {
	if !s.isConfigServer(name) {
		return false
	}
	s.detachAndCloseServer(name)
	s.logger.Info("server removed by config change", "server", name)
	return true
}

func (s *Server) connectConfigServer(ctx context.Context, sc config.ServerConfig) (toolsDropped bool) {
	switch {
	case !s.isKnownServer(sc.Name):
		s.logger.Info("server added by config change", "server", sc.Name)
		s.ConnectUpstreams(ctx, []config.ServerConfig{sc})
	case s.settingsChanged(sc) && !s.loginInFlightFor(sc):
		s.logger.Info("reconnecting server with its changed settings", "server", sc.Name)
		s.reconnectConfigServer(ctx, sc)
		return true
	case s.loginSavedSinceAuthFailed(sc.Name):
		s.logger.Info("reconnecting server with its saved login", "server", sc.Name)
		s.reconnectConfigServer(ctx, sc)
		return true
	}
	return false
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

func (s *Server) recordSettings(sc config.ServerConfig) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if record, ok := s.configServers[sc.Name]; ok {
		record.settings = sc
		s.configServers[sc.Name] = record
	}
}

func (s *Server) settingsChanged(sc config.ServerConfig) bool {
	s.stateMu.RLock()
	record, recorded := s.configServers[sc.Name]
	s.stateMu.RUnlock()
	return recorded && !sameSettings(record.settings, sc)
}

// sameSettings ignores what applies without a reconnect, and UnsetEnv, which only explains a failure.
func sameSettings(a, b config.ServerConfig) bool {
	for _, sc := range []*config.ServerConfig{&a, &b} {
		sc.Projections, sc.ProjectionsErr, sc.UnsetEnv, sc.Enabled = nil, nil, nil, nil
	}
	// Fields added later compare too: a needless reconnect costs seconds, a missed edit leaves old
	// settings running.
	return reflect.DeepEqual(a, b)
}

// start_auth's login fills in Auth in place as it discovers endpoints; a shared copy would make every
// check see an edit.
func withOwnAuth(sc config.ServerConfig) config.ServerConfig {
	sc.Auth = sc.Auth.Clone()
	return sc
}

func (s *Server) reconnectConfigServer(ctx context.Context, sc config.ServerConfig) {
	// A login still waiting in the browser was started for the old settings.
	s.cancelExistingAuthFlow(sc.Name)
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

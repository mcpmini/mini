//go:build test

package server

import (
	"time"

	"github.com/mcpmini/mini/internal/config"
)

// recordConfigServers marks each enabled server as started from the config, without connecting it.
// A server not running yet gets an incarnation whose startup hold has already passed.
func (s *Server) recordConfigServers(servers []config.ServerConfig) {
	for _, sc := range servers {
		if !sc.IsEnabled() {
			continue
		}
		if u := s.upstreams.get(sc.Name); u != nil {
			u.fromConfig, u.saved = true, withOwnAuth(sc)
			continue
		}
		u := s.startupInstall(sc)
		u.openedAt = time.Time{}
		s.upstreams.swap(sc.Name, u)
	}
	s.startupChanged.signal()
}

// openConnectWindow starts a new startup hold for name, as a config edit does by replacing it.
func (s *Server) openConnectWindow(name string) {
	sc := config.ServerConfig{Name: name}
	if u := s.upstreams.get(name); u != nil {
		sc = u.cfg
	}
	s.upstreams.swap(
		name,
		s.newUpstreamServer(sc, upstreamOrigin{fromConfig: true}),
	)
	s.startupChanged.signal()
}

// startupInstall returns the incarnation a startup connect for sc would run on.
func (s *Server) startupInstall(sc config.ServerConfig) *upstreamServer {
	if u := s.upstreams.get(sc.Name); u != nil {
		return u
	}
	return s.newUpstreamServer(sc, upstreamOrigin{fromConfig: true})
}

func (s *Server) liveProjections(name string) map[string]*config.ProjectionConfig {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.projections[name]
}

func (s *Server) waitForUpstreamGoroutines() {
	for _, u := range s.upstreams.snapshot() {
		u.group.Wait()
	}
}

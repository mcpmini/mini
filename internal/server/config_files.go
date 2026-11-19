package server

import (
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func (s *Server) loginSavedAt(name string) time.Time {
	info, err := os.Stat(auth.TokenPath(s.configDir, name))
	if err != nil {
		return time.Time{} // no saved login yet
	}
	return info.ModTime()
}

// applyServerFiles connects and removes servers to match servers/. It looks at every name each tick,
// so a name skipped while busy or broken is picked up once it isn't.
func (s *Server) applyServerFiles() {
	names, err := s.namesToApply()
	if err != nil {
		return // a listing error must never remove servers; the next tick tries again
	}
	toolsDropped := false
	for _, name := range names {
		toolsDropped = s.applyServerFile(name) || toolsDropped
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
	names := append(s.upstreams.configNames(), saved...)
	slices.Sort(names)
	return slices.Compact(names), nil
}

func (s *Server) applyServerFile(name string) (toolsDropped bool) {
	unlock, ok := s.serverNames.tryLock(name)
	if !ok {
		return false
	}
	defer unlock()
	return s.reconcileLocked(name)
}

// reconcileLocked makes what runs under name match its file, under name's lock. It reports whether
// tools were dropped, which a caller tells agents about.
func (s *Server) reconcileLocked(name string) (toolsDropped bool) {
	sc, saved := s.lookupSavedServer(name)
	switch saved {
	case savedGone:
		return s.removeConfigServer(name)
	case savedUnreadable:
		return false
	case savedStartable:
	}
	return s.connectConfigServer(sc)
}

type savedState int

const (
	savedStartable  savedState = iota
	savedGone                  // missing or disabled
	savedUnreadable            // left as it runs until the file can be read
)

func (s *Server) lookupSavedServer(name string) (config.ServerConfig, savedState) {
	sc, found, err := config.LookupServer(s.configDir, name)
	switch {
	case err != nil:
		return sc, savedUnreadable
	case !found || !sc.IsEnabled():
		return sc, savedGone
	case sc.Command == "" && sc.URL == "": // an empty or half-written file decodes without error
		return sc, savedUnreadable
	}
	return sc, savedStartable
}

func (s *Server) removeConfigServer(name string) bool {
	if !s.isConfigServer(name) {
		return false
	}
	s.remove(name, retireRemoved)
	s.logger.Info("server removed by config change", "server", name)
	return true
}

func (s *Server) connectConfigServer(sc config.ServerConfig) (toolsDropped bool) {
	switch {
	case !s.isKnownServer(sc.Name):
		s.logger.Info("server added by config change", "server", sc.Name)
		s.replace(sc.Name, s.newConfigServer(sc))
	case s.settingsChanged(sc) && !s.loginInFlightFor(sc):
		s.logger.Info("reconnecting server with its changed settings", "server", sc.Name)
		s.replace(sc.Name, s.newConfigServer(sc))
		return true
	case s.loginSavedSinceAuthFailed(sc.Name):
		s.logger.Info("reconnecting server with its saved login", "server", sc.Name)
		s.replace(sc.Name, s.newConfigServer(sc))
		return true
	}
	return false
}

func (s *Server) loginSavedSinceAuthFailed(name string) bool {
	if s.hasAuthFlow(name) { // a login start_auth runs reconnects the server itself
		return false
	}
	u := s.upstreams.get(name)
	if u == nil || !u.fromConfig {
		return false
	}
	_, needsAuth := u.startupState(s.clock.Now()).status.(needsAuthStatus)
	return needsAuth && s.loginSavedAt(name).After(u.loginSavedAtFailure())
}

func (s *Server) settingsChanged(sc config.ServerConfig) bool {
	u := s.upstreams.get(sc.Name)
	return u != nil && u.fromConfig && !sameSettings(u.savedSettings(), sc)
}

func (u *upstreamServer) loginSavedAtFailure() time.Time {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.loginSavedAt
}

func (u *upstreamServer) savedSettings() config.ServerConfig {
	u.mu.RLock()
	defer u.mu.RUnlock()
	saved := u.saved
	if saved.Auth == nil {
		saved.Auth = u.detectedAuth
	}
	return saved
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

// The record outlives the config it came from, whose Auth pointer goes on to the connect and the
// login; anything writing through it would make every check see an edit.
func withOwnAuth(sc config.ServerConfig) config.ServerConfig {
	sc.Auth = sc.Auth.Clone()
	return sc
}

func (s *Server) isKnownServer(name string) bool {
	return s.upstreams.get(name) != nil
}

func (s *Server) isConfigServer(name string) bool {
	u := s.upstreams.get(name)
	return u != nil && u.fromConfig
}

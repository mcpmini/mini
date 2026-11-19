package server

import "fmt"

type retireReason int

const (
	retireRemoved  retireReason = iota // the server is gone: its projections go too
	retireReplaced                     // a new incarnation follows: projections stay
)

func (r retireReason) loginCancelReason() error {
	if r == retireRemoved {
		return errLoginServerGone
	}
	return errLoginSettingsEdit
}

// replace starts next connecting in the background, after stopping what ran under name.
func (s *Server) replace(name string, next *upstreamServer) {
	if s.swapIn(name, next, retireReplaced) {
		s.start(next)
	}
}

// replaceConnected installs u with a connection already open.
func (s *Server) replaceConnected(u *upstreamServer, c connected) error {
	if !s.swapIn(u.cfg.Name, u, retireReplaced) {
		c.conn.Close() //nolint:errcheck // The install is refused; Close only releases the unused connection.
		return fmt.Errorf("server %q: %w", u.cfg.Name, errClosed)
	}
	s.adoptConn(u, c.conn, c.tools)
	return nil
}

func (s *Server) remove(name string, reason retireReason) {
	s.swapIn(name, nil, reason)
}

// swapIn reports false when mini is shutting down and next was not installed.
func (s *Server) swapIn(name string, next *upstreamServer, reason retireReason) bool {
	// A server starting under a free name leaves a login started for it running, and keeps the
	// provider a synchronous Open just attached to next's connection.
	retiring := next == nil || s.upstreams.get(name) != nil
	if retiring {
		// Before the swap: an install checks the generation and swaps under serverOpMu, so one
		// that started before this either lands first, and is retired here, or is refused.
		s.bumpRemoveGen(name)
	}
	prev, ok := s.upstreams.swap(name, next)
	if !ok {
		return false
	}
	if retiring || prev != nil {
		s.retire(name, prev, reason)
	}
	s.startupChanged.signal()
	if next != nil {
		s.seedProjectionsIfNone(next.cfg)
	}
	return true
}

// retire ends what runs under name. u is nil when no incarnation runs, and a pending login, a
// provider or projections may still be left.
func (s *Server) retire(name string, u *upstreamServer, reason retireReason) {
	s.cancelExistingAuthFlow(name, reason.loginCancelReason())
	if u != nil {
		u.beginStop()
	}
	// A token refresh runs under the provider's lifetime, not the incarnation's, so only Forget
	// ends one; the second Forget drops a provider a dying attempt re-created meanwhile.
	s.providerRegistry.Forget(name)
	if u != nil {
		u.closeConn()
		u.awaitStopped()
	}
	s.providerRegistry.Forget(name)
	s.reg.RemoveServer(name)
	s.sessions.closeServerConnections(name)
	if reason == retireRemoved {
		s.stateMu.Lock()
		delete(s.projections, name)
		s.stateMu.Unlock()
	}
}

// A login finishing after its server was retired must not save its token.
func (s *Server) bumpRemoveGen(name string) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	s.removeGen[name]++
}

func (s *Server) snapshotRemoveGen(name string) uint64 {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	return s.removeGen[name]
}

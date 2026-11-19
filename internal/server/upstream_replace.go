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

// replace starts next connecting in the background. It, replaceConnected,
// replaceReportingFirstOpen and remove are the only ways the set of running incarnations changes
// before Close, each called under the name's lock. They stop the previous incarnation before
// starting the next, so the two never run at once.
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

// firstOpen is a new incarnation's first connect, which a login reports; after a failure a retry
// can fix, the open loop goes on retrying.
type firstOpen struct {
	u      *upstreamServer
	result chan error
}

// wait reports no failure for an incarnation retired meanwhile: what retired it handles the server.
func (o *firstOpen) wait() error {
	err := <-o.result
	if o.u.group.Stopped() {
		return nil
	}
	return err
}

func (s *Server) replaceReportingFirstOpen(name string, next *upstreamServer) *firstOpen {
	opening := &firstOpen{u: next, result: make(chan error, 1)}
	started := s.swapIn(name, next, retireReplaced) &&
		s.startOpenLoop(next, openLoopParams{immediate: true, firstOpen: opening.result})
	if !started {
		opening.result <- fmt.Errorf("server %q: %w", name, errClosed)
	}
	return opening
}

func (s *Server) remove(name string, reason retireReason) {
	s.swapIn(name, nil, reason)
}

// swapIn reports false when mini is shutting down and next was not installed.
func (s *Server) swapIn(name string, next *upstreamServer, reason retireReason) bool {
	prev, ok := s.upstreams.swap(name, next)
	if !ok {
		return false
	}
	// Retiring with nothing to replace would forget the provider a synchronous Open just attached to
	// next's connection, which then refuses to authorize.
	if prev != nil || next == nil {
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
	s.bumpRemoveGen(name)
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

// A login finishing after its server was retired must not save its token, so every retire moves
// the name's generation on before the incarnation stops.
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

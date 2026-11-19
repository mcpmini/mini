package server

import (
	"context"
	"errors"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// start opens the incarnation's first connection in the background, retrying until it connects,
// fails for good, or is stopped.
func (s *Server) start(u *upstreamServer) {
	s.startOpenLoop(u, openLoopParams{immediate: true})
}

// maybeReconnect restarts the open loop after a call or a refresh failed on the connection. The old
// connection keeps serving until a new one is adopted.
func (s *Server) maybeReconnect(u *upstreamServer, err error) {
	if err == nil || !isConnError(err) || errors.Is(err, transport.ErrReauthRequired) {
		return
	}
	s.startOpenLoop(u, openLoopParams{})
}

type openLoopParams struct {
	immediate bool // the first attempt doesn't wait out a backoff
}

func (s *Server) startOpenLoop(u *upstreamServer, p openLoopParams) {
	u.mu.Lock()
	defer u.mu.Unlock()
	// A failure a retry can't fix ends the loop for good: only a replace, once its remedy is
	// applied, opens the server again.
	if u.opening || u.failure != nil {
		return
	}
	u.opening = u.group.Go(func() { s.runOpenLoop(u, p) })
}

func (s *Server) runOpenLoop(u *upstreamServer, p openLoopParams) {
	defer s.endOpening(u)
	backoff := time.Second
	if !p.immediate {
		if !s.sleepBackoff(u.ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
	for {
		conn, tools, err := s.openUpstream(u.ctx, u.cfg)
		if err == nil {
			s.adoptConn(u, conn, tools)
			return
		}
		if !s.retryOpenAfter(u, err, backoff) || !s.sleepBackoff(u.ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (s *Server) endOpening(u *upstreamServer) {
	u.mu.Lock()
	u.opening = false
	u.mu.Unlock()
	s.startupChanged.signal()
}

func (u *upstreamServer) isOpening() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.opening
}

func (s *Server) retryOpenAfter(u *upstreamServer, err error, backoff time.Duration) bool {
	name := u.cfg.Name
	if u.ctx.Err() != nil {
		return false
	}
	if failure, terminal := terminalStartupFailure(err); terminal {
		s.logger.Warn(failure.logMessage(), "server", name, "err", err)
		s.recordFailure(u, failure)
		return false
	}
	if u.currentConn() == nil {
		s.logger.Warn("upstream unavailable at startup, retrying", "server", name, "err", err, "backoff", backoff)
	} else {
		s.logger.Warn("reconnect failed", "server", name, "err", err, "backoff", backoff)
	}
	return true
}

func (s *Server) sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := s.clock.NewTimer(d)
	select {
	case <-ctx.Done():
		t.Stop()
		return false
	case <-t.Chan():
		return true
	}
}

// nextBackoff doubles d up to a 64s ceiling (1s → 2s → … → 32s → 64s → 64s…).
func nextBackoff(d time.Duration) time.Duration {
	if d < 64*time.Second {
		return d * 2
	}
	return d
}

// adoptConn makes conn the incarnation's connection and publishes its tools, unless the incarnation
// was stopped meanwhile. The publish happens under u.mu after checking the stop, so nothing is
// published once stop has begun.
func (s *Server) adoptConn(u *upstreamServer, conn transport.Connection, toolList transport.ToolList) {
	u.mu.Lock()
	if u.group.Stopped() {
		u.mu.Unlock()
		conn.Close() //nolint:errcheck // The incarnation was stopped; Close only releases the unused connection.
		return
	}
	old, hook := u.conn, u.onReconnect
	u.conn = conn
	u.adoptToolList(toolList)
	s.publishTools(u, toolList.Tools)
	u.ready = true
	u.mu.Unlock()
	s.startupChanged.signal()
	// Outside u.mu: HTTP Close waits for notification callbacks that take u.mu.
	if old != nil {
		old.Close() //nolint:errcheck // The replacement is installed; Close only releases the stale connection.
	}
	s.attachNotificationHandler(u, conn)
	s.notifyAllSessions()
	s.logger.Info("upstream connected", "server", u.cfg.Name, "tools", len(toolList.Tools))
	if old != nil && hook != nil {
		hook()
	}
}

// recordFailure records a failure a retry can't fix. The incarnation's tools stay published; for a
// configured server, calls get the failure's remedy and the watcher replaces it once that remedy is
// applied.
func (s *Server) recordFailure(u *upstreamServer, failure startupFailure) {
	loginSavedAt := s.loginSavedAt(u.cfg.Name)
	detected := u.cfg
	config.MergeDetectedOAuth(s.configDir, &detected)
	u.mu.Lock()
	defer s.startupChanged.signal()
	defer u.mu.Unlock()
	if u.group.Stopped() {
		return
	}
	u.ready = false
	u.failure = failure
	u.loginSavedAt = loginSavedAt
	u.detectedAuth = detected.Auth
}

// beginStop stops the incarnation publishing or starting work, and cancels what runs. Its connection
// stays open until closeConn: HTTP Close waits for the notification listener, which may be in a
// token refresh that only ending the provider stops.
func (u *upstreamServer) beginStop() {
	u.mu.Lock()
	u.group.markStopped()
	u.mu.Unlock()
	u.refresher.stop()
	u.cancel()
}

func (u *upstreamServer) closeConn() {
	u.mu.RLock()
	conn := u.conn
	u.mu.RUnlock()
	// Outside u.mu: HTTP Close waits for notification callbacks that take u.mu.
	if conn != nil {
		conn.Close() //nolint:errcheck // Stopping has no caller to receive connection cleanup errors.
	}
}

func (u *upstreamServer) awaitStopped() { u.group.Wait() }

// stop ends an incarnation another one replaces: its provider is the new one's, so it stays.
func (u *upstreamServer) stop() {
	u.beginStop()
	u.closeConn()
	u.awaitStopped()
}

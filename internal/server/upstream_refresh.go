package server

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

// Some upstreams take well over 30 seconds to list their tools. A refresh that outlasts the
// discovery wait still publishes, and agents learn of the new list through list_changed.
const refreshTimeout = 60 * time.Second

// Codex gives each server 30 seconds to start and list its tools, so a discovery read answers
// within that, startup hold included, and serves the list it has.
const discoveryWait = 25 * time.Second

func (s *Server) attachNotificationHandler(u *upstreamServer, conn transport.Connection) {
	source, ok := conn.(transport.NotificationSource)
	if !ok {
		return
	}
	source.SetNotificationHandler(func(notification transport.Notification) {
		if notification.Method == transport.NotificationToolsChanged {
			s.requestToolRefresh(u, conn)
		}
	})
}

func (s *Server) requestToolRefresh(u *upstreamServer, conn transport.Connection) {
	if s.isCurrentUpstreamConn(u, conn) {
		u.refresher.request()
	}
}

func (s *Server) awaitCurrentToolLists(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, discoveryWait)
	defer cancel()
	if err := s.waitForStartup(ctx); err != nil {
		return err
	}
	s.awaitFreshToolLists(wait, s.snapshotConnectedUpstreams())
	return ctx.Err()
}

func (s *Server) awaitFreshToolLists(wait context.Context, upstreams []*upstreamServer) {
	refreshes := make([]<-chan struct{}, 0, len(upstreams))
	for _, u := range upstreams {
		refreshes = append(refreshes, u.refresher.refreshIfStale(s.clock.Now()))
	}
	for _, done := range refreshes {
		select {
		case <-done:
		case <-wait.Done():
			return
		case <-s.stopWaiting:
			return
		}
	}
}

func (s *Server) runToolRefreshes(u *upstreamServer) {
	for u.refresher.next() {
		if err := s.refreshTools(u); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("refresh upstream tools failed", "server", u.cfg.Name, "err", err)
		}
	}
}

func (s *Server) refreshTools(u *upstreamServer) error {
	conn := s.currentUpstreamConn(u)
	if conn == nil {
		return context.Canceled
	}
	ctx, cancel := context.WithTimeout(u.ctx, refreshTimeout)
	defer cancel()
	toolList, err := conn.ListTools(ctx)
	if err != nil {
		if transport.IsConnectionError(err) {
			s.maybeReconnect(u, markConnError(err))
		}
		return err
	}
	if !s.publishRefreshedTools(u, conn, toolList) {
		return context.Canceled
	}
	return nil
}

func (s *Server) publishRefreshedTools(
	u *upstreamServer,
	conn transport.Connection,
	toolList transport.ToolList,
) bool {
	// Under u.mu, not serverOpMu: a remove holds serverOpMu while it waits for this refresh to end.
	u.mu.Lock()
	if !s.isCurrentUpstreamConnLocked(u, conn) {
		u.mu.Unlock()
		return false
	}
	changed := !reflect.DeepEqual(u.lastDefs, toolList.Tools)
	u.adoptToolList(toolList)
	s.publishTools(u, toolList.Tools)
	u.mu.Unlock()
	if changed {
		s.notifyAllSessions()
	}
	return true
}

func (s *Server) currentUpstreamConn(u *upstreamServer) transport.Connection {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if s.isCurrentUpstreamConnLocked(u, u.conn) {
		return u.conn
	}
	return nil
}

func (s *Server) isCurrentUpstreamConn(u *upstreamServer, conn transport.Connection) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return s.isCurrentUpstreamConnLocked(u, conn)
}

func (s *Server) isCurrentUpstreamConnLocked(u *upstreamServer, conn transport.Connection) bool {
	if conn == nil || u.group.Stopped() || u.conn != conn {
		return false
	}
	return s.upstreams.get(u.cfg.Name) == u
}

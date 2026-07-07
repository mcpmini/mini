package server

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

const refreshTimeout = 15 * time.Second

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
	if !s.isCurrentUpstreamConn(u, conn) {
		return
	}
	if !u.beginRefreshRequest() {
		return
	}
	s.refreshWg.Add(1)
	go s.runToolRefreshes(u)
}

func (s *Server) runToolRefreshes(u *upstreamServer) {
	defer s.refreshWg.Done()
	for s.takeRefreshRequest(u) {
		if err := s.refreshTools(u); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("refresh upstream tools failed", "server", u.cfg.Name, "err", err)
		}
	}
}

func (s *Server) takeRefreshRequest(u *upstreamServer) bool {
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	if !u.refreshPending || u.ctx.Err() != nil {
		u.refreshing = false
		return false
	}
	u.refreshPending = false
	return true
}

func (u *upstreamServer) beginRefreshRequest() bool {
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	u.refreshPending = true
	if u.refreshing {
		return false
	}
	u.refreshing = true
	return true
}

func (s *Server) refreshTools(u *upstreamServer) error {
	conn := s.currentUpstreamConn(u)
	if conn == nil {
		return context.Canceled
	}
	ctx, cancel := context.WithTimeout(u.ctx, refreshTimeout)
	defer cancel()
	tools, err := conn.ListTools(ctx)
	if err != nil {
		if transport.IsConnectionError(err) {
			s.maybeReconnect(u, connError{err: err})
		}
		return err
	}
	if !s.publishRefreshedTools(u, conn, tools) {
		return context.Canceled
	}
	return nil
}

func (s *Server) publishRefreshedTools(u *upstreamServer, conn transport.Connection, tools []transport.ToolDefinition) bool {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if !s.refreshConnStillCurrent(u, conn) {
		return false
	}
	changed := !reflect.DeepEqual(u.lastDefs, tools)
	u.lastDefs = tools
	s.reg.ReplaceServerTools(registry.ServerParams{
		Name:            u.cfg.Name,
		Defs:            tools,
		Perm:            u.cfg.Permissions,
		AliasByToolName: s.currentAliasesFor(u.cfg.Name),
	})
	if changed {
		s.notifyAllSessions()
	}
	return true
}

func (s *Server) refreshConnStillCurrent(u *upstreamServer, conn transport.Connection) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return s.isCurrentUpstreamConnLocked(u, conn)
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
	if conn == nil || u.ctx.Err() != nil || u.conn != conn {
		return false
	}
	s.mu.RLock()
	current := s.upstreams[u.cfg.Name] == u
	s.mu.RUnlock()
	return current
}

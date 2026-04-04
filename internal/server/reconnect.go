package server

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) reconnectLoop(u *upstreamServer) {
	defer u.reconnecting.Store(false)
	backoff := time.Second
	for {
		t := s.clock.NewTimer(backoff)
		select {
		case <-u.ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
		s.logger.Info("reconnecting upstream", "server", u.cfg.Name, "backoff", backoff)
		if s.tryReconnect(u) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// nextBackoff doubles d up to a 64s ceiling (1s → 2s → … → 32s → 64s → 64s…).
func nextBackoff(d time.Duration) time.Duration {
	if d < 64*time.Second {
		return d * 2
	}
	return d
}

func (s *Server) tryReconnect(u *upstreamServer) bool {
	conn, tools, err := s.dialAndList(u)
	if err != nil {
		return false
	}
	return s.swapConn(u, conn, tools)
}

func (s *Server) dialAndList(u *upstreamServer) (transport.Connection, []transport.ToolDefinition, error) {
	ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
	conn, err := dialUpstream(ctx, s.logger, s.cfg, u.cfg)
	cancel()
	if err != nil {
		s.logger.Warn("reconnect failed", "server", u.cfg.Name, "err", err)
		return nil, nil, err
	}
	listCtx, listCancel := context.WithTimeout(u.ctx, 15*time.Second)
	tools, err := conn.ListTools(listCtx)
	listCancel()
	if err != nil {
		conn.Close()
		s.logger.Warn("reconnect list tools failed", "server", u.cfg.Name, "err", err)
		return nil, nil, err
	}
	return conn, tools, nil
}

func (s *Server) swapConn(u *upstreamServer, conn transport.Connection, tools []transport.ToolDefinition) bool {
	u.mu.Lock()
	select {
	case <-u.ctx.Done():
		u.mu.Unlock()
		conn.Close()
		return false
	default:
	}
	old, hook := u.conn, u.onReconnect
	u.conn = conn
	u.mu.Unlock()
	if old != nil {
		old.Close()
	}
	s.reg.ReplaceServer(u.cfg.Name, tools, u.cfg.Permissions)
	s.logger.Info("upstream reconnected", "server", u.cfg.Name)
	if hook != nil {
		hook()
	}
	return true
}

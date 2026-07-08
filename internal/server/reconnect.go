package server

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) maybeReconnect(upstream *upstreamServer, err error) {
	if err == nil || !isConnError(err) {
		return
	}
	// Skip if upstream is already shutting down. callConn releases u.mu.RLock
	// before returning, so there is a narrow window where Close() can complete
	// reconnectWg.Wait() before this goroutine calls reconnectWg.Add(1). The
	// WaitGroup won't panic (w==0 when Wait already returned), but the goroutine
	// would run briefly after Close() returns. Checking Err() prevents that.
	if upstream.ctx.Err() != nil {
		return
	}
	if !upstream.reconnecting.CompareAndSwap(false, true) {
		return
	}
	s.spawnReconnect(upstream)
}

func (s *Server) spawnReconnect(upstream *upstreamServer) {
	s.reconnectWg.Add(1)
	go func() {
		defer s.reconnectWg.Done()
		s.reconnectLoop(upstream)
	}()
}

func (s *Server) reconnectLoop(u *upstreamServer) {
	defer u.reconnecting.Store(false)
	backoff := time.Second
	for {
		if !s.sleepBackoff(u, backoff) {
			return
		}
		s.logger.Info("reconnecting upstream", "server", u.cfg.Name, "backoff", backoff)
		if s.tryReconnect(u) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (s *Server) sleepBackoff(u *upstreamServer, d time.Duration) bool {
	t := s.clock.NewTimer(d)
	select {
	case <-u.ctx.Done():
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

func (s *Server) tryReconnect(u *upstreamServer) bool {
	conn, tools, err := s.dialAndList(u)
	if err != nil {
		return false
	}
	return s.swapConn(u, conn, tools)
}

func (s *Server) dialAndList(u *upstreamServer) (transport.Connection, []transport.ToolDefinition, error) {
	conn, err := s.dialForReconnect(u)
	if err != nil {
		return nil, nil, err
	}
	tools, err := s.listToolsForReconnect(u, conn)
	if err != nil {
		return nil, nil, err
	}
	return conn, tools, nil
}

func (s *Server) dialForReconnect(u *upstreamServer) (transport.Connection, error) {
	ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
	conn, err := s.dialUpstream(ctx, u.cfg)
	cancel()
	if err != nil {
		s.logger.Warn("reconnect failed", "server", u.cfg.Name, "err", err)
	}
	return conn, err
}

func (s *Server) listToolsForReconnect(u *upstreamServer, conn transport.Connection) ([]transport.ToolDefinition, error) {
	ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
	defer cancel()
	tools, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close()
		err = s.markOAuthIfRequired(ctx, u.cfg, err)
		s.logger.Warn("reconnect list tools failed", "server", u.cfg.Name, "err", err)
	}
	return tools, err
}

func (s *Server) swapConn(u *upstreamServer, conn transport.Connection, tools []transport.ToolDefinition) bool {
	old, hook, ok := swapReconnectConn(u, conn)
	if !ok {
		return false
	}
	s.attachNotificationHandler(u, conn)
	if old != nil {
		old.Close()
	}
	s.replaceRegistryToolsLocked(u, tools)
	s.notifyAllSessions()
	s.logger.Info("upstream reconnected", "server", u.cfg.Name)
	if hook != nil {
		hook()
	}
	return true
}

func (s *Server) replaceRegistryToolsLocked(u *upstreamServer, tools []transport.ToolDefinition) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	u.lastDefs = tools
	s.reg.ReplaceServer(registry.ServerParams{
		Name:    u.cfg.Name,
		Defs:    tools,
		Perm:    u.cfg.Permissions,
		AliasByToolName: s.currentAliasesFor(u.cfg.Name),
	})
}

func swapReconnectConn(u *upstreamServer, conn transport.Connection) (transport.Connection, func(), bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	select {
	case <-u.ctx.Done():
		conn.Close()
		return nil, nil, false
	default:
	}
	old, hook := u.conn, u.onReconnect
	u.conn = conn
	return old, hook, true
}

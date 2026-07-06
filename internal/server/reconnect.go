package server

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) maybeReconnect(upstream *upstreamServer, err error) {
	if err == nil || !isConnError(err) {
		return
	}
	// Skip if upstream is already shutting down. Calls snapshot the current
	// connection pointer before returning, so there is a narrow window where
	// Close() can complete reconnectWg.Wait() before this goroutine calls
	// reconnectWg.Add(1). The WaitGroup won't panic (w==0 when Wait already
	// returned), but the goroutine would run briefly after Close() returns.
	// Checking Err() prevents that.
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
	swap := swapReconnectConn(u, conn)
	if !swap.ok {
		return false
	}
	if swap.old != nil {
		swap.old.Close()
	}
	s.attachNotificationHandler(u, conn, swap.gen)
	s.catalog.replaceCurrent(u, swap.gen, tools)
	s.logger.Info("upstream reconnected", "server", u.cfg.Name)
	if swap.hook != nil {
		swap.hook()
	}
	return true
}

type reconnectSwap struct {
	old  transport.Connection
	hook func()
	gen  uint64
	ok   bool
}

func swapReconnectConn(u *upstreamServer, conn transport.Connection) reconnectSwap {
	prev, gen, ok := u.replaceConn(conn)
	if !ok {
		return reconnectSwap{}
	}
	return reconnectSwap{
		old:  reconnectConn(prev),
		hook: u.reconnectHook(),
		gen:  gen,
		ok:   true,
	}
}

func reconnectConn(state *upstreamConnState) transport.Connection {
	if state == nil {
		return nil
	}
	return state.conn
}

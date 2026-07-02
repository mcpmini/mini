package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

type dispatchParams struct {
	Upstream *upstreamServer
	Tool     string
	Params   map[string]any
	Session  *Session
}

func (s *Server) dispatchRaw(ctx context.Context, p dispatchParams) (json.RawMessage, int64, error) {
	ctx, cancel := applyToolTimeout(ctx, p.Upstream.cfg.ToolTimeout)
	defer cancel()
	start := s.clock.Now()
	raw, err := s.dispatchRawCall(ctx, p)
	return raw, s.clock.Since(start).Milliseconds(), err
}

func (s *Server) dispatchRawCall(ctx context.Context, p dispatchParams) (json.RawMessage, error) {
	if p.Upstream.cfg.SessionMode == config.SessionModePerSession {
		return s.callPerSession(ctx, p)
	}
	raw, err := p.Upstream.callTool(ctx, p.Tool, p.Params)
	s.maybeReconnect(p.Upstream, err)
	return raw, err
}

func (s *Server) callPerSession(ctx context.Context, p dispatchParams) (json.RawMessage, error) {
	conn, err := s.getOrDialSessionConn(ctx, p.Upstream, p.Session)
	if err != nil {
		return nil, fmt.Errorf("per_session dial: %w", err)
	}
	args, _ := json.Marshal(transport.ToolCallParams{Name: p.Tool, Arguments: p.Params})
	raw, err := conn.Call(ctx, "tools/call", args)
	if err != nil {
		return nil, s.handleSessionConnErr(p.Upstream, p.Session, conn, err)
	}
	result, toolErr := invoke.ExtractContent(raw)
	return result, toolErr
}

func (s *Server) handleSessionConnErr(upstream *upstreamServer, session *Session, conn transport.Connection, err error) error {
	var rpcErr *transport.RPCError
	if errors.As(err, &rpcErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	s.logger.Warn("per-session connection error", "server", upstream.cfg.Name, "err", err)
	session.EvictConn(upstream.cfg.Name, conn)
	conn.Close()
	return connError{err}
}

func (s *Server) getOrDialSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.Conn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, err := session.dialOnceFor(upstream.cfg.Name, func() (transport.Connection, error) {
		return s.dialPerSessionConn(ctx, upstream, session)
	})
	if err != nil {
		return nil, err
	}
	return s.checkDialedConn(upstream.cfg.Name, conn, session)
}

func (s *Server) checkDialedConn(name string, conn transport.Connection, session *Session) (transport.Connection, error) {
	if s.isUpstreamRegistered(name) {
		return conn, nil
	}
	session.RemoveConn(name)
	conn.Close()
	return nil, fmt.Errorf("server %q removed during dial", name)
}

func (s *Server) dialPerSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.Conn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, err := s.dialUpstream(ctx, upstream.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ListTools(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init per_session conn: %w", err)
	}
	return session.GetOrSetConn(upstream.cfg.Name, conn), nil
}

func (s *Server) isUpstreamRegistered(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.upstreams[name]
	return ok
}

func (s *Server) getUpstream(serverName string) (*upstreamServer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.upstreams[serverName]
	if !ok {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}
	return u, nil
}

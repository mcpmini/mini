package server

import (
	"context"
	"encoding/json"
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
	MRTR     *transport.ToolCallMRTR
}

func (s *Server) dispatchRaw(ctx context.Context, p dispatchParams) (json.RawMessage, int64, error) {
	ctx, cancel := applyToolTimeout(ctx, p.Upstream.cfg.ToolTimeout)
	defer cancel()
	start := s.clock.Now()
	raw, err := s.dispatchRawCall(ctx, p)
	return raw, s.clock.Since(start).Milliseconds(), invoke.ExplainInputRequired(p.Upstream.cfg.Name, err)
}

func (s *Server) dispatchRawCall(ctx context.Context, p dispatchParams) (json.RawMessage, error) {
	if p.Upstream.cfg.SessionMode == config.SessionModePerSession {
		return s.callPerSession(ctx, p)
	}
	raw, err := p.Upstream.callTool(ctx, callToolParams{Tool: p.Tool, Args: p.Params, MRTR: p.MRTR})
	s.maybeReconnect(p.Upstream, err)
	return raw, err
}

func (s *Server) callPerSession(ctx context.Context, p dispatchParams) (json.RawMessage, error) {
	args, err := json.Marshal(transport.NewToolCallParams(p.Tool, p.Params, p.MRTR))
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	conn, err := s.getOrDialSessionConn(ctx, p.Upstream, p.Session)
	if err != nil {
		return nil, fmt.Errorf("per_session dial: %w", err)
	}
	raw, err := conn.Call(ctx, "tools/call", args)
	if err != nil {
		return nil, s.handleSessionConnErr(sessionConnErrParams{
			Upstream: p.Upstream, Session: p.Session, Conn: conn, Err: err,
		})
	}
	return raw, invoke.CheckResult(raw)
}

type sessionConnErrParams struct {
	Upstream *upstreamServer
	Session  *Session
	Conn     transport.Connection
	Err      error
}

func (s *Server) handleSessionConnErr(p sessionConnErrParams) error {
	err := classifyCallError(p.Err)
	if !isConnError(err) {
		return err
	}
	s.logger.Warn("per-session connection error", "server", p.Upstream.cfg.Name, "err", p.Err)
	p.Session.EvictConn(p.Upstream.cfg.Name, p.Conn)
	p.Conn.Close() //nolint:errcheck // The call error is returned; Close only releases the evicted connection.
	return err
}

func (s *Server) getOrDialSessionConn(
	ctx context.Context,
	upstream *upstreamServer,
	session *Session,
) (transport.Connection, error) {
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

func (s *Server) checkDialedConn(
	name string,
	conn transport.Connection,
	session *Session,
) (transport.Connection, error) {
	if s.isUpstreamConnected(name) {
		return conn, nil
	}
	session.RemoveConn(name)
	conn.Close() //nolint:errcheck // The removal error is returned; Close only releases the stale connection.
	return nil, fmt.Errorf("server %q removed during dial", name)
}

func (s *Server) dialPerSessionConn(
	ctx context.Context,
	upstream *upstreamServer,
	session *Session,
) (transport.Connection, error) {
	if conn := session.Conn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, _, err := s.openUpstream(ctx, upstream.cfg)
	if err != nil {
		return nil, err
	}
	return session.GetOrSetConn(upstream.cfg.Name, conn), nil
}

func (s *Server) isUpstreamConnected(name string) bool {
	u := s.upstreams.get(name)
	return u != nil && u.currentConn() != nil
}

func (s *Server) getUpstream(serverName string) (*upstreamServer, error) {
	u := s.upstreams.get(serverName)
	if u == nil {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}
	return u, nil
}

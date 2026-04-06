package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) AddUpstream(ctx context.Context, sc config.ServerConfig) error {
	conn, err := dialUpstream(ctx, s.logger, s.cfg, sc)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", sc.Name, err)
	}
	return s.registerUpstream(ctx, sc, conn)
}

func (s *Server) AddConnection(ctx context.Context, sc config.ServerConfig, conn transport.Connection) error {
	return s.registerUpstream(ctx, sc, conn)
}

func dialUpstream(ctx context.Context, logger *slog.Logger, cfg *config.Config, sc config.ServerConfig) (transport.Connection, error) {
	switch sc.Transport {
	case "http", "sse", "streamable":
		return transport.NewHTTPConnection(transport.HTTPConnectionConfig{
			URL:                     sc.URL,
			Headers:                 mergedHeaders(sc),
			ClientTimeout:           parseHTTPClientTimeout(sc.HTTPClientTimeout),
			DisableRetryOnRateLimit: sc.DisableRetryOnRateLimit,
		})
	default:
		return transport.NewStdioConnection(ctx, logger, sc.Command, sc.Args, sc.Env)
	}
}

func parseHTTPClientTimeout(spec string) time.Duration {
	if spec == "" || spec == "0" {
		return 0
	}
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func mergedHeaders(sc config.ServerConfig) map[string]string {
	headers := make(map[string]string)
	for k, v := range sc.Headers {
		headers[k] = resolveHeaderValue(v)
	}
	if sc.Auth != nil {
		applyAuthHeader(headers, sc.Auth)
	}
	return headers
}

func applyAuthHeader(headers map[string]string, auth *config.AuthConfig) {
	token := expandEnv(auth.Token)
	if token == "" {
		return
	}
	name := auth.Header
	if name == "" {
		name = "Authorization"
	}
	if auth.Type == "apikey" {
		headers[name] = token
		return
	}
	headers[name] = "Bearer " + token
}

// SetReconnectHook sets a callback that fires after a successful automatic reconnect
// for the named server. Used in tests to replace polling with a deterministic signal.
func (s *Server) SetReconnectHook(serverName string, fn func()) {
	s.mu.RLock()
	u := s.upstreams[serverName]
	s.mu.RUnlock()
	if u == nil {
		return
	}
	u.mu.Lock()
	u.onReconnect = fn
	u.mu.Unlock()
}

// IsReconnecting reports whether the named server is currently in a reconnect loop.
// Used in tests to assert that application-level errors do not trigger reconnects.
func (s *Server) IsReconnecting(serverName string) bool {
	s.mu.RLock()
	u := s.upstreams[serverName]
	s.mu.RUnlock()
	return u != nil && u.reconnecting.Load()
}

func (s *Server) registerUpstream(ctx context.Context, sc config.ServerConfig, conn transport.Connection) error {
	tools, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("list tools from %s: %w", sc.Name, err)
	}
	u := newUpstreamServer(sc, conn)
	old := s.swapUpstream(sc.Name, u)
	s.registerTools(sc, tools, old)
	if sc.Projections != nil {
		s.mu.Lock()
		s.projections[sc.Name] = sc.Projections
		s.mu.Unlock()
	}
	s.logger.Info("upstream registered", "server", sc.Name, "tools", len(tools))
	return nil
}

func newUpstreamServer(sc config.ServerConfig, conn transport.Connection) *upstreamServer {
	ctx, cancel := context.WithCancel(context.Background())
	u := &upstreamServer{cfg: sc, conn: conn, ctx: ctx, cancel: cancel}
	if sc.MaxPendingRequests > 0 {
		u.sem = make(chan struct{}, sc.MaxPendingRequests)
	}
	return u
}

func (s *Server) swapUpstream(name string, u *upstreamServer) *upstreamServer {
	s.mu.Lock()
	old := s.upstreams[name]
	s.upstreams[name] = u
	s.mu.Unlock()
	return old
}

func (s *Server) registerTools(sc config.ServerConfig, tools []transport.ToolDefinition, old *upstreamServer) {
	if old != nil {
		old.shutdown()
		old.mu.Lock()
		old.conn.Close()
		old.mu.Unlock()
		s.reg.ReplaceServer(sc.Name, tools, sc.Permissions)
		return
	}
	s.reg.AddServer(sc.Name, tools, sc.Permissions)
}

// Must be called in a goroutine; blocks until ctx is canceled.
func (s *Server) RunSessionEviction(ctx context.Context, maxIdle time.Duration) {
	ticker := time.NewTicker(maxIdle / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.sessions.evictIdle(maxIdle)
			s.evictIdleRateLimiters()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) Close() {
	s.authMu.Lock()
	flows := s.authFlows
	s.authFlows = make(map[string]*authFlowState)
	s.authMu.Unlock()
	for _, f := range flows {
		f.cancel()
	}

	s.mu.Lock()
	upstreams := make([]*upstreamServer, 0, len(s.upstreams))
	for _, u := range s.upstreams {
		upstreams = append(upstreams, u)
	}
	s.mu.Unlock()
	for _, u := range upstreams {
		u.shutdown()
		u.mu.Lock()
		u.conn.Close()
		u.mu.Unlock()
	}
	s.store.Close()
}

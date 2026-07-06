package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) AddUpstream(ctx context.Context, sc config.ServerConfig) error {
	conn, err := s.dialUpstream(ctx, sc)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", sc.Name, err)
	}
	if err := s.registerUpstream(ctx, sc, conn); err != nil {
		return s.markOAuthIfRequired(ctx, sc, err)
	}
	return nil
}

func (s *Server) markOAuthIfRequired(ctx context.Context, sc config.ServerConfig, connErr error) error {
	// RuntimeAdded servers could collide by name with an existing server — never write for them.
	// A manually-configured header is decisive too: RFC 6750 gives an expired static token the
	// same 401 challenge as real OAuth, so a hand-set header means the user already chose.
	if sc.RuntimeAdded || sc.Auth != nil || !sc.IsHTTPTransport() || len(sc.Headers) > 0 {
		return connErr
	}
	// Already marked: skip re-running the PRM probe and rewriting the marker on every
	// reconnect backoff cycle against a persistently-401 upstream.
	if config.IsOAuthDetected(s.configDir, sc.Name) {
		return oauthRequiredError(sc.Name, connErr)
	}
	var uerr *transport.UnauthorizedError
	if !errors.As(connErr, &uerr) || !auth.RequiresOAuth(ctx, sc.URL, uerr.WWWAuthenticate) {
		return connErr
	}
	if err := config.MarkOAuthDetected(s.configDir, sc.Name); err != nil {
		s.logger.Warn("persist discovered oauth requirement", "server", sc.Name, "err", err)
		return connErr
	}
	return oauthRequiredError(sc.Name, connErr)
}

func oauthRequiredError(serverName string, connErr error) error {
	return fmt.Errorf("%s requires OAuth authorization (discovered via 401); run `mini auth %s`: %w", serverName, serverName, connErr)
}

func (s *Server) AddConnection(ctx context.Context, sc config.ServerConfig, conn transport.Connection) error {
	return s.registerUpstream(ctx, sc, conn)
}

func (s *Server) dialUpstream(ctx context.Context, sc config.ServerConfig) (transport.Connection, error) {
	return invoke.Dial(ctx, invoke.DialParams{Logger: s.logger, Config: s.cfg, Server: sc, Clock: s.clock})
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
	u.hookMu.Lock()
	u.onReconnect = fn
	u.hookMu.Unlock()
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
	gen := s.catalog.snapshotRemovalGeneration(sc.Name)
	tools, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("list tools from %s: %w", sc.Name, err)
	}
	u, err := s.catalog.installIfCurrent(sc, conn, tools, gen)
	if err != nil {
		return err
	}
	s.attachNotificationHandler(u, conn, u.currentConnGen())
	return nil
}

func (s *Server) installUpstream(sc config.ServerConfig, conn transport.Connection, tools []transport.ToolDefinition) *upstreamServer {
	u := newUpstreamServer(sc, conn, s.clock)
	u.lastDefs = tools
	old := s.swapUpstream(sc.Name, u)
	s.registerTools(sc, tools, old)
	if sc.Projections != nil {
		s.mu.Lock()
		s.projections[sc.Name] = sc.Projections
		s.mu.Unlock()
	}
	s.logger.Info("upstream registered", "server", sc.Name, "tools", len(tools))
	return u
}

func newUpstreamServer(sc config.ServerConfig, conn transport.Connection, clock clock.Clock) *upstreamServer {
	ctx, cancel := context.WithCancel(context.Background())
	u := &upstreamServer{cfg: sc, ctx: ctx, cancel: cancel, clock: clock}
	u.initConn(conn)
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
	p := registry.ServerParams{Name: sc.Name, Defs: tools, Perm: sc.Permissions, AliasByToolName: config.AliasesFromProjections(sc.Projections)}
	if old != nil {
		old.shutdownAndClose()
		s.reg.ReplaceServerTools(p)
		return
	}
	s.reg.AddServer(p)
}

// currentAliasesFor returns the alias map from the live, reload-updated
// projections — unlike the install-time sc.Projections snapshot, this
// reflects any config reload since the server was added.
func (s *Server) currentAliasesFor(serverName string) map[string]string {
	s.mu.RLock()
	proj := s.projections[serverName]
	s.mu.RUnlock()
	return config.AliasesFromProjections(proj)
}

// Must be called in a goroutine; blocks until ctx is canceled.
func (s *Server) RunSessionEviction(ctx context.Context, maxIdle time.Duration) {
	s.runSessionEviction(ctx, maxIdle, nil)
}

func (s *Server) runSessionEviction(ctx context.Context, maxIdle time.Duration, afterEvict func()) {
	ticker := s.clock.NewTicker(maxIdle / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			s.sessions.evictIdle(s.clock.Now().Add(-maxIdle))
			if afterEvict != nil {
				afterEvict()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) BeginShutdown() {
	s.shutdownOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.closing = true
		s.lifecycleMu.Unlock()
		s.sessions.closeNotificationStreams()
	})
}

func (s *Server) Close() {
	s.closeOnce.Do(s.close)
}

func (s *Server) close() {
	s.BeginShutdown()
	cancelAuthFlows(s.takeAuthFlows())
	s.authWg.Wait()
	closeUpstreams(s.snapshotUpstreams())
	s.refreshWg.Wait()
	s.reconnectWg.Wait()
	s.sessions.closeAll()
	s.store.Close()
}

func (s *Server) takeAuthFlows() map[string]*authFlowState {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	flows := s.authFlows
	s.authFlows = make(map[string]*authFlowState)
	return flows
}

func cancelAuthFlows(flows map[string]*authFlowState) {
	for _, f := range flows {
		f.cancel()
	}
}

func (s *Server) snapshotUpstreams() []*upstreamServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	upstreams := make([]*upstreamServer, 0, len(s.upstreams))
	for _, u := range s.upstreams {
		upstreams = append(upstreams, u)
	}
	return upstreams
}

func closeUpstreams(upstreams []*upstreamServer) {
	for _, u := range upstreams {
		u.shutdownAndClose()
	}
}

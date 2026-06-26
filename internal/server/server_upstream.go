package server

import (
	"context"
	"fmt"
	"time"

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
	return s.registerUpstream(ctx, sc, conn)
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
	gen := s.snapshotRemoveGen(sc.Name)
	tools, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("list tools from %s: %w", sc.Name, err)
	}
	return s.installIfNotRemoved(sc, conn, tools, gen)
}

func (s *Server) snapshotRemoveGen(name string) uint64 {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	return s.removeGen[name]
}

func (s *Server) installIfNotRemoved(sc config.ServerConfig, conn transport.Connection, tools []transport.ToolDefinition, gen uint64) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if s.removeGen[sc.Name] != gen {
		conn.Close()
		return fmt.Errorf("server %q was removed during connection setup", sc.Name)
	}
	s.installUpstreamLocked(sc, conn, tools)
	return nil
}

func (s *Server) installUpstreamLocked(sc config.ServerConfig, conn transport.Connection, tools []transport.ToolDefinition) {
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
}

func newUpstreamServer(sc config.ServerConfig, conn transport.Connection, appClock clock.Clock) *upstreamServer {
	ctx, cancel := context.WithCancel(context.Background())
	u := &upstreamServer{cfg: sc, conn: conn, ctx: ctx, cancel: cancel, clock: appClock}
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
		s.reg.ReplaceServer(p)
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
	ticker := s.clock.NewTicker(maxIdle / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			s.sessions.evictIdle(s.clock.Now().Add(-maxIdle))
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) Close() {
	cancelAuthFlows(s.takeAuthFlows())
	s.authWg.Wait()
	closeUpstreams(s.snapshotUpstreams())
	s.reconnectWg.Wait()
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

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

func (s *Server) ConnectUpstreams(ctx context.Context, servers []config.ServerConfig) {
	if s.cancelConnect != nil {
		s.cancelConnect()
	}
	connectCtx, cancel := context.WithCancel(ctx)
	s.cancelConnect = cancel
	for _, sc := range servers {
		if !sc.IsEnabled() {
			continue
		}
		s.connectWg.Add(1)
		go s.connectUpstreamAsync(connectCtx, s.startupInstall(sc))
	}
}

var (
	errServerRemoved     = errors.New("removed during connection setup")
	errAlreadyRegistered = errors.New("already registered")
)

type upstreamInstall struct {
	cfg          config.ServerConfig
	removeGen    uint64
	keepExisting bool
}

func (s *Server) startupInstall(sc config.ServerConfig) upstreamInstall {
	return upstreamInstall{cfg: sc, removeGen: s.snapshotRemoveGen(sc.Name), keepExisting: true}
}

func (s *Server) replacingInstall(sc config.ServerConfig) upstreamInstall {
	return upstreamInstall{cfg: sc, removeGen: s.snapshotRemoveGen(sc.Name)}
}

func (s *Server) connectUpstreamAsync(ctx context.Context, in upstreamInstall) {
	defer s.connectWg.Done()
	backoff := time.Second
	for {
		err := s.connectAtStartup(ctx, in)
		if err == nil {
			s.notifyAllSessions()
			return
		}
		if !s.retryStartupAfter(in.cfg.Name, err, backoff) || !s.sleepBackoff(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (s *Server) connectAtStartup(ctx context.Context, in upstreamInstall) error {
	if err := s.checkInstall(in); err != nil {
		return err
	}
	return s.addUpstream(ctx, in)
}

func (s *Server) retryStartupAfter(name string, err error, backoff time.Duration) bool {
	switch {
	case errors.Is(err, errServerRemoved), errors.Is(err, errAlreadyRegistered):
		s.logger.Info("startup connect abandoned", "server", name, "reason", err)
		return false
	case errors.Is(err, transport.ErrReauthRequired):
		s.logger.Warn("upstream needs authorization, not retrying", "server", name, "err", err)
		return false
	}
	s.logger.Warn("upstream unavailable at startup, retrying", "server", name, "err", err, "backoff", backoff)
	return true
}

func (s *Server) AddUpstream(ctx context.Context, sc config.ServerConfig) error {
	return s.addUpstream(ctx, s.replacingInstall(sc))
}

func (s *Server) addUpstream(ctx context.Context, in upstreamInstall) error {
	connectCtx, cancel := applyHandshakeTimeout(ctx, in.cfg.HandshakeTimeout)
	defer cancel()
	conn, err := s.dialUpstream(connectCtx, in.cfg)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", in.cfg.Name, err)
	}
	if err := s.registerUpstream(connectCtx, conn, in); err != nil {
		return s.markOAuthIfRequired(ctx, in.cfg, err)
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
	return fmt.Errorf("%s requires OAuth authorization; run `mini auth %s`: %w: %w", serverName, serverName, transport.ErrReauthRequired, connErr)
}

func (s *Server) AddConnection(ctx context.Context, sc config.ServerConfig, conn transport.Connection) error {
	return s.registerUpstream(ctx, conn, s.replacingInstall(sc))
}

func (s *Server) dialUpstream(ctx context.Context, sc config.ServerConfig) (transport.Connection, error) {
	return invoke.Dial(ctx, invoke.DialParams{
		Logger: s.logger, Config: s.cfg, Server: sc, Clock: s.clock,
		ConfigDir: s.configDir, ProviderRegistry: s.providerRegistry,
	})
}

// SetReconnectHook sets a callback that fires after a successful automatic reconnect
// for the named server. Used in tests to replace polling with a deterministic signal.
func (s *Server) SetReconnectHook(serverName string, fn func()) {
	s.stateMu.RLock()
	u := s.upstreams[serverName]
	s.stateMu.RUnlock()
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
	s.stateMu.RLock()
	u := s.upstreams[serverName]
	s.stateMu.RUnlock()
	return u != nil && u.reconnecting.Load()
}

func (s *Server) registerUpstream(ctx context.Context, conn transport.Connection, in upstreamInstall) error {
	tools, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("list tools from %s: %w", in.cfg.Name, err)
	}
	return s.installChecked(conn, tools, in)
}

func (s *Server) snapshotRemoveGen(name string) uint64 {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	return s.removeGen[name]
}

func (s *Server) checkInstall(in upstreamInstall) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	return s.checkInstallLocked(in)
}

func (s *Server) installChecked(conn transport.Connection, tools []transport.ToolDefinition, in upstreamInstall) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if err := s.checkInstallLocked(in); err != nil {
		conn.Close()
		return err
	}
	s.installUpstreamLocked(in.cfg, conn, tools)
	return nil
}

func (s *Server) checkInstallLocked(in upstreamInstall) error {
	if s.removeGen[in.cfg.Name] != in.removeGen {
		return fmt.Errorf("server %q: %w", in.cfg.Name, errServerRemoved)
	}
	if !in.keepExisting {
		return nil
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.upstreams[in.cfg.Name] != nil {
		return fmt.Errorf("server %q: %w", in.cfg.Name, errAlreadyRegistered)
	}
	return nil
}

func (s *Server) installUpstreamLocked(sc config.ServerConfig, conn transport.Connection, tools []transport.ToolDefinition) {
	u := newUpstreamServer(sc, conn, s.clock)
	u.lastDefs = tools
	old := s.swapUpstream(sc.Name, u)
	s.registerTools(sc, tools, old)
	s.attachNotificationHandler(u, conn)
	if sc.Projections != nil {
		s.stateMu.Lock()
		if s.projections[sc.Name] == nil {
			s.projections[sc.Name] = sc.Projections
		}
		s.stateMu.Unlock()
	}
	s.logger.Info("upstream registered", "server", sc.Name, "tools", len(tools))
}

func newUpstreamServer(sc config.ServerConfig, conn transport.Connection, clock clock.Clock) *upstreamServer {
	ctx, cancel := context.WithCancel(context.Background())
	u := &upstreamServer{cfg: sc, conn: conn, ctx: ctx, cancel: cancel, clock: clock}
	if sc.MaxPendingRequests > 0 {
		u.sem = make(chan struct{}, sc.MaxPendingRequests)
	}
	return u
}

func (s *Server) swapUpstream(name string, u *upstreamServer) *upstreamServer {
	s.stateMu.Lock()
	old := s.upstreams[name]
	s.upstreams[name] = u
	s.stateMu.Unlock()
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
	s.stateMu.RLock()
	proj := s.projections[serverName]
	s.stateMu.RUnlock()
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

func (s *Server) Close() {
	cancelAuthFlows(s.takeAuthFlows())
	// caller's ctx may still be live (e.g. deferred Close runs before signal cancel)
	if s.cancelConnect != nil {
		s.cancelConnect()
	}
	s.authWg.Wait()
	s.providerRegistry.Close()
	s.connectWg.Wait()
	closeUpstreams(s.snapshotUpstreams())
	s.sessions.closeAll()
	s.refreshWg.Wait()
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
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
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

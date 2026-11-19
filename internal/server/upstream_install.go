package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) ConnectUpstreams(servers []config.ServerConfig) {
	for _, sc := range servers {
		if !sc.IsEnabled() {
			continue
		}
		if s.upstreams.get(sc.Name) != nil {
			s.logger.Info("startup connect skipped, the server is already running", "server", sc.Name)
			continue
		}
		u := s.newUpstreamServer(sc, upstreamOrigin{fromConfig: true})
		if _, ok := s.upstreams.swap(sc.Name, u); !ok {
			return
		}
		s.startupChanged.signal()
		s.start(u)
	}
}

var (
	errServerRemoved     = errors.New("removed during connection setup")
	errAlreadyRegistered = errors.New("already registered")
	errClosed            = errors.New("mini is shutting down")
)

type upstreamInstall struct {
	cfg             config.ServerConfig
	removeGen       uint64
	refuseIfRunning bool
	fromConfig      bool
}

func (s *Server) newServerInstall(sc config.ServerConfig) upstreamInstall {
	return upstreamInstall{cfg: sc, removeGen: s.snapshotRemoveGen(sc.Name), refuseIfRunning: true, fromConfig: true}
}

func (s *Server) replacingInstall(sc config.ServerConfig) upstreamInstall {
	return upstreamInstall{cfg: sc, removeGen: s.snapshotRemoveGen(sc.Name)}
}

func (s *Server) AddUpstream(ctx context.Context, sc config.ServerConfig) error {
	return s.addUpstream(ctx, s.replacingInstall(sc))
}

func (s *Server) addUpstream(ctx context.Context, in upstreamInstall) error {
	conn, toolList, err := s.openUpstream(ctx, in.cfg)
	if err != nil {
		return err
	}
	return s.installChecked(conn, toolList, in)
}

func (s *Server) AddConnection(ctx context.Context, sc config.ServerConfig, conn transport.Connection) error {
	return s.registerUpstream(ctx, conn, s.replacingInstall(sc))
}

func (s *Server) openUpstream(
	ctx context.Context,
	sc config.ServerConfig,
) (transport.Connection, transport.ToolList, error) {
	return invoke.Open(ctx, invoke.OpenParams{DialParams: invoke.DialParams{
		Logger: s.logger, Config: s.cfg, Server: sc, Clock: s.clock,
		ConfigDir: s.configDir, ProviderRegistry: s.providerRegistry,
	}})
}

// SetReconnectHook sets a callback that fires after a successful automatic reconnect
// for the named server. Used in tests to replace polling with a deterministic signal.
func (s *Server) SetReconnectHook(serverName string, fn func()) {
	u := s.upstreams.get(serverName)
	if u == nil {
		return
	}
	u.mu.Lock()
	u.onReconnect = fn
	u.mu.Unlock()
}

// IsReconnecting reports whether the named server's open loop is running.
// Used in tests to assert that application-level errors do not trigger reconnects.
func (s *Server) IsReconnecting(serverName string) bool {
	u := s.upstreams.get(serverName)
	return u != nil && u.isOpening()
}

func (s *Server) registerUpstream(ctx context.Context, conn transport.Connection, in upstreamInstall) error {
	toolList, err := conn.ListTools(ctx)
	if err != nil {
		conn.Close() //nolint:errcheck // ListTools failure is returned; Close only releases the unregistered connection.
		return fmt.Errorf("list tools from %s: %w", in.cfg.Name, err)
	}
	return s.installChecked(conn, toolList, in)
}

func (s *Server) snapshotRemoveGen(name string) uint64 {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	return s.removeGen[name]
}

func (s *Server) installChecked(conn transport.Connection, toolList transport.ToolList, in upstreamInstall) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if err := s.checkInstallLocked(in); err != nil {
		conn.Close() //nolint:errcheck // The install rejection is returned; Close only releases the unregistered connection.
		return err
	}
	return s.installUpstreamLocked(in, conn, toolList)
}

func (s *Server) checkInstallLocked(in upstreamInstall) error {
	if s.removeGen[in.cfg.Name] != in.removeGen {
		return fmt.Errorf("server %q: %w", in.cfg.Name, errServerRemoved)
	}
	if in.refuseIfRunning && s.upstreams.get(in.cfg.Name) != nil {
		return fmt.Errorf("server %q: %w", in.cfg.Name, errAlreadyRegistered)
	}
	return nil
}

func (s *Server) installUpstreamLocked(
	in upstreamInstall,
	conn transport.Connection,
	toolList transport.ToolList,
) error {
	prev := s.upstreams.get(in.cfg.Name)
	// A login's reconnect replaces a configured server, which stays one.
	fromConfig := in.fromConfig || (prev != nil && prev.fromConfig)
	u := s.newUpstreamServer(in.cfg, upstreamOrigin{fromConfig: fromConfig})
	if _, ok := s.upstreams.swap(in.cfg.Name, u); !ok {
		conn.Close() //nolint:errcheck // The install is refused; Close only releases the unused connection.
		return fmt.Errorf("server %q: %w", in.cfg.Name, errClosed)
	}
	if prev != nil {
		prev.stop()
	}
	s.seedProjectionsIfNone(in.cfg)
	s.adoptConn(u, conn, toolList)
	return nil
}

func (s *Server) seedProjectionsIfNone(sc config.ServerConfig) {
	if sc.Projections == nil {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.projections[sc.Name] == nil {
		s.projections[sc.Name] = sc.Projections
	}
}

type upstreamOrigin struct {
	fromConfig bool
}

func (s *Server) newUpstreamServer(sc config.ServerConfig, origin upstreamOrigin) *upstreamServer {
	ctx, cancel := context.WithCancel(context.Background())
	u := &upstreamServer{
		cfg: sc, fromConfig: origin.fromConfig, saved: withOwnAuth(sc),
		openedAt: s.clock.Now(), ctx: ctx, cancel: cancel, clock: s.clock,
	}
	u.refresher.startWorker = func() bool { return u.group.Go(func() { s.runToolRefreshes(u.ctx, u) }) }
	if sc.MaxPendingRequests > 0 {
		u.sem = make(chan struct{}, sc.MaxPendingRequests)
	}
	return u
}

// publishTools is called under u.mu after checking the incarnation isn't stopped, so a remove,
// which stops it before taking its tools out, can't race this write.
func (s *Server) publishTools(u *upstreamServer, defs []transport.ToolDefinition) {
	s.reg.ReplaceServer(registry.ServerParams{
		Name:            u.cfg.Name,
		Defs:            defs,
		Perm:            u.cfg.Permissions,
		AliasByToolName: s.currentAliasesFor(u.cfg.Name),
	})
}

func (s *Server) currentAliasesFor(serverName string) map[string]string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return config.AliasesFromProjections(
		s.projections[serverName],
	) // walked under the lock: set_projection writes into this map
}

func (s *Server) detachAndCloseServer(serverName string) {
	s.cancelExistingAuthFlow(serverName, errLoginServerGone)
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	s.detachAndCloseLocked(serverName)
}

func (s *Server) detachAndCloseLocked(serverName string) {
	s.removeGen[serverName]++
	s.closeDetached(serverName, s.detachUpstream(serverName))
}

func (s *Server) closeDetached(serverName string, u *upstreamServer) {
	if u != nil {
		u.beginStop()
	}
	// A token refresh runs under the provider's lifetime, not the incarnation's, so only Forget
	// ends one; the second Forget drops a provider a dying attempt re-created meanwhile.
	s.providerRegistry.Forget(serverName)
	if u != nil {
		u.closeConn()
		u.awaitStopped()
	}
	s.providerRegistry.Forget(serverName)
	s.sessions.closeServerConnections(serverName)
	s.reg.RemoveServer(serverName)
}

func (s *Server) detachUpstream(serverName string) *upstreamServer {
	s.stateMu.Lock()
	delete(s.projections, serverName)
	s.stateMu.Unlock()
	return s.detachUpstreamKeepingProjections(serverName)
}

func (s *Server) detachUpstreamKeepingProjections(serverName string) *upstreamServer {
	u, _ := s.upstreams.swap(serverName, nil) // a remove is never refused
	s.startupChanged.signal()
	return u
}

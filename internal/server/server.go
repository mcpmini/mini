package server

import (
	"log/slog"
	"slices"
	"sync"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

type Server struct {
	cfg              *config.Config
	configDir        string
	reg              *registry.Registry
	upstreams        *upstreamSet
	startupChanged   *broadcast // signaled on every change of an incarnation's startup state
	stopWaiting      chan struct{}
	stopWaitingOnce  sync.Once
	projections      map[string]map[string]*config.ProjectionConfig
	store            *response.Store
	projDefaults     *projection.Defaults
	sessions         *sessionStore
	logger           *slog.Logger
	clock            clock.Clock
	daemonAuthToken  string
	providerRegistry *provider.Registry
	// Held across each add_server, remove_server and config check of one name, so its saved and live states agree.
	serverNames nameLocks
	// Lock ordering: serverNames → persistMu → serverOpMu → u.mu → stateMu → authMu; upstreamSet.mu
	// and an incarnation's goroutineGroup are leaves.
	// stateMu is the innermost hot-path lock (RLock on every request);
	// the outer locks serialize cold-path admin operations.
	stateMu    sync.RWMutex
	persistMu  sync.Mutex
	serverOpMu sync.Mutex
	// Protected by serverOpMu. Bumped on every retire, so a login started before one can't save its
	// token.
	removeGen  map[string]uint64
	authMu     sync.Mutex
	authFlows  map[string]*authFlowState
	authWg     sync.WaitGroup
	reloadLoop backgroundLoop
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

func (s *Server) Close() {
	s.reloadLoop.stop()
	s.ReleaseStartupHolds()
	cancelAuthFlows(s.takeAuthFlows())
	upstreams := s.upstreams.close()
	for _, u := range upstreams {
		u.beginStop()
	}
	s.authWg.Wait()
	// Before closing and waiting: a token refresh runs under the provider registry's lifetime, not
	// an incarnation's.
	s.providerRegistry.Close()
	for _, u := range upstreams {
		u.closeConn()
		u.awaitStopped()
	}
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
	return s.upstreams.snapshot()
}

func (s *Server) snapshotConnectedUpstreams() []*upstreamServer {
	upstreams := s.upstreams.snapshot()
	return slices.DeleteFunc(upstreams, func(u *upstreamServer) bool { return u.currentConn() == nil })
}

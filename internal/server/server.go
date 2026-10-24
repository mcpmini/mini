package server

import (
	"log/slog"
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
	upstreams        map[string]*upstreamServer
	configServers    map[string]configServer // servers started from the config files, the only ones a config edit removes
	startup          *startupTracker         // guarded by stateMu
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
	// Lock ordering: serverNames → persistMu → serverOpMu → stateMu → authMu.
	// stateMu is the innermost hot-path lock (RLock on every request);
	// the outer locks serialize cold-path admin operations.
	stateMu     sync.RWMutex
	persistMu   sync.Mutex
	serverOpMu  sync.Mutex
	removeGen   map[string]uint64 // protected by serverOpMu; incremented on each remove_server
	authMu      sync.Mutex
	authFlows   map[string]*authFlowState
	authWg      sync.WaitGroup
	reconnectWg sync.WaitGroup // tracks all active reconnectLoop goroutines
	refreshWg   sync.WaitGroup
	connector   *upstreamConnector
	reloadLoop  backgroundLoop
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

func (s *Server) Close() {
	s.reloadLoop.stop()
	s.ReleaseStartupHolds()
	cancelAuthFlows(s.takeAuthFlows())
	s.connector.stop()
	s.authWg.Wait()
	s.providerRegistry.Close()
	s.connector.wait()
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

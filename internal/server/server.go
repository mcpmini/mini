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
	"github.com/mcpmini/mini/internal/transport"
)

type Server struct {
	cfg                  *config.Config
	configDir            string
	reg                  *registry.Registry
	upstreams            map[string]*upstreamServer
	configServers        map[string]bool // servers started from the config files, the only ones a config edit removes
	projections          map[string]map[string]*config.ProjectionConfig
	envelope             *response.Builder
	store                *response.Store
	projDefaults         *projection.Defaults
	toolSchemas          []map[string]any
	sessions             *sessionStore
	logger               *slog.Logger
	clock                clock.Clock
	toolMode             transport.ToolMode
	daemonAuthToken      string
	allowNonLoopbackHost bool
	providerRegistry     *provider.Registry
	// Lock ordering: persistMu → serverOpMu → stateMu → authMu.
	// stateMu is the innermost hot-path lock (RLock on every request);
	// the outer locks serialize cold-path admin operations.
	stateMu         sync.RWMutex
	persistMu       sync.Mutex
	serverOpMu      sync.Mutex        // serializes concurrent add_server / remove_server for the same name
	removeGen       map[string]uint64 // protected by serverOpMu; incremented on each remove_server
	authMu          sync.Mutex
	authFlows       map[string]*authFlowState
	authWg          sync.WaitGroup
	reconnectWg     sync.WaitGroup // tracks all active reconnectLoop goroutines
	refreshWg       sync.WaitGroup
	pendingConnects *pendingConnects
}

func (s *Server) notifyAllSessions() {
	for _, sess := range s.sessions.snapshotSessions() {
		sess.notifyToolsChanged()
	}
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

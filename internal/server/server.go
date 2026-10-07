package server

import (
	"log/slog"
	"sync"
	"time"

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
	connectStartedAt     map[string]time.Time
	toolsReady           map[string]bool // set after the registry has the tools; upstreams is set before
	startupFailures      map[string]startupFailure
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
	// Held across each add_server, remove_server and reload removal of one name, so its saved and live states agree.
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
}

func (s *Server) notifyAllSessions() {
	for _, sess := range s.sessions.snapshotSessions() {
		sess.notifyToolsChanged()
	}
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

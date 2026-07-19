package server

import (
	"context"
	"log/slog"
	"sync"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

type ServerOption func(*Server)

// WithClock replaces the server's clock. Used in tests to inject a fake clock
// so reconnect backoff timers fire immediately without real sleeps.
func WithClock(clock clock.Clock) ServerOption {
	return func(s *Server) { s.clock = clock }
}

func WithToolMode(m transport.ToolMode) ServerOption {
	return func(s *Server) { s.toolMode = m }
}

// WithDaemonAuthToken requires Authorization: Bearer <token> on the /mcp endpoint.
// Only the daemon sets this; the stdio and serve --http paths leave it empty so
// their existing clients (which send no token) keep working.
func WithDaemonAuthToken(token string) ServerOption {
	return func(s *Server) { s.daemonAuthToken = token }
}

// WithAllowNonLoopbackHost disables the loopback-Host (DNS-rebinding) check on /mcp.
// Set only when the operator explicitly binds the HTTP server to a non-loopback address
// (--dangerous-nonloopback-http), where remote clients legitimately send a non-loopback Host.
func WithAllowNonLoopbackHost() ServerOption {
	return func(s *Server) { s.allowNonLoopbackHost = true }
}

// WithAuthProviders makes OAuth2 upstreams dial with a dynamic AuthorizationProvider
// (proactive expiry-based refresh, 401 replay) instead of a statically-applied bearer
// header. Set only by the long-lived serve paths (connect, daemon); one-shot CLI
// commands (status, test) keep the static inject-and-refresh behavior so there is
// exactly one process-wide refresh owner per long-lived server.
func WithAuthProviders() ServerOption {
	return func(s *Server) { s.useAuthProviders = true }
}

type Server struct {
	cfg                  *config.Config
	configDir            string
	reg                  *registry.Registry
	upstreams            map[string]*upstreamServer
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
	useAuthProviders     bool
	providerCache        *auth.ProviderCache
	// Lock ordering: persistMu → serverOpMu → stateMu → authMu.
	// stateMu is the innermost hot-path lock (RLock on every request);
	// the outer locks serialize cold-path admin operations.
	stateMu     sync.RWMutex
	persistMu   sync.Mutex
	serverOpMu  sync.Mutex        // serializes concurrent add_server / remove_server for the same name
	removeGen   map[string]uint64 // protected by serverOpMu; incremented on each remove_server
	authMu      sync.Mutex
	authFlows   map[string]*authFlowState
	authWg        sync.WaitGroup
	reconnectWg   sync.WaitGroup // tracks all active reconnectLoop goroutines
	refreshWg     sync.WaitGroup
	connectWg     sync.WaitGroup
	cancelConnect context.CancelFunc
}

func (s *Server) notifyAllSessions() {
	for _, sess := range s.sessions.snapshotSessions() {
		sess.notifyToolsChanged()
	}
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

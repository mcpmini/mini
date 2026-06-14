package server

import (
	"log/slog"
	"sync"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

type ServerOption func(*Server)

// WithClock replaces the server's clock. Used in tests to inject a fake clock
// so reconnect backoff timers fire immediately without real sleeps.
func WithClock(c clock.Clock) ServerOption {
	return func(s *Server) { s.clock = c }
}

// WithToolMode sets the server's default tool mode for standalone runs.
// In passthrough mode (the default) upstream tools are exposed directly as
// server__tool instead of behind the 4-tool abstraction, and mini does not
// apply perm_call; clients such as Claude configure per-tool approval/allow
// rules against the exposed upstream tool names.
func WithToolMode(m ToolMode) ServerOption {
	return func(s *Server) { s.toolMode = m }
}

type Server struct {
	cfg          *config.Config
	configDir    string
	reg          *registry.Registry
	upstreams    map[string]*upstreamServer
	projections  map[string]map[string]*config.ProjectionConfig
	envelope     *response.Builder
	store        *response.Store
	projDefaults *projection.Defaults
	toolSchemas  []map[string]any
	sessions     *sessionStore
	logger       *slog.Logger
	clock        clock.Clock
	toolMode     ToolMode
	// Lock ordering: when both mu and authMu must be acquired, always acquire mu first.
	mu          sync.RWMutex
	persistMu   sync.Mutex
	serverOpMu  sync.Mutex // serializes concurrent add_server / remove_server for the same name
	removeGen   map[string]uint64 // protected by serverOpMu; incremented on each remove_server
	authMu      sync.Mutex
	authFlows   map[string]*authFlowState
	authWg      sync.WaitGroup
	reconnectWg sync.WaitGroup // tracks all active reconnectLoop goroutines
}

func (s *Server) notifyAllSessions() {
	for _, sess := range s.sessions.snapshotSessions() {
		sess.notify(toolsChangedNotif)
	}
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

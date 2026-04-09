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
	// Lock ordering: when both mu and authMu must be acquired, always acquire mu first.
	mu          sync.RWMutex
	persistMu   sync.Mutex
	authMu      sync.Mutex
	authFlows   map[string]*authFlowState
	reconnectWg sync.WaitGroup // tracks all active reconnectLoop goroutines
}

func (s *Server) ToolCount(serverName string) int {
	return s.reg.ToolCount(serverName)
}

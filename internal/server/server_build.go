package server

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

// Params configures a new Server. Config, ConfigDir, and Logger are required.
type Params struct {
	Config               *config.Config
	ConfigDir            string
	Logger               *slog.Logger
	Clock                clock.Clock
	ToolMode             transport.ToolMode
	DaemonAuthToken      string
	AllowNonLoopbackHost bool
}

func New(p Params) *Server {
	requireParams(p)
	servers, err := config.LoadServers(p.ConfigDir)
	if err != nil {
		p.Logger.Warn("starting without projections", "err", err)
	}
	s := newServer(p.Config, p.ConfigDir, serverProjections(servers.Loaded), p.Logger)
	applyParams(s, p)
	store := mustStore(p.Config, p.ConfigDir, p.Logger, s.clock)
	s.store = store
	s.envelope = response.NewBuilder(store)
	s.sessions = newSessionStore(s.clock)
	return s
}

func requireParams(p Params) {
	if p.Config == nil {
		panic("server.Params.Config must not be nil")
	}
	if p.ConfigDir == "" {
		panic("server.Params.ConfigDir must not be empty")
	}
	if p.Logger == nil {
		panic("server.Params.Logger must not be nil")
	}
}

func applyParams(s *Server, p Params) {
	if p.Clock != nil {
		s.clock = p.Clock
	}
	s.toolMode = p.ToolMode
	s.daemonAuthToken = p.DaemonAuthToken
	s.allowNonLoopbackHost = p.AllowNonLoopbackHost
}

func newServer(cfg *config.Config, configDir string, projections map[string]map[string]*config.ProjectionConfig, logger *slog.Logger) *Server {
	s := &Server{
		cfg:              cfg,
		configDir:        configDir,
		reg:              registry.New(),
		upstreams:        make(map[string]*upstreamServer),
		configServers:    make(map[string]bool),
		removeGen:        make(map[string]uint64),
		projections:      projections,
		projDefaults:     projection.DefaultsFrom(cfg),
		toolSchemas:      compactToolSchemas(),
		authFlows:        make(map[string]*authFlowState),
		logger:           logger,
		clock:            clock.System(),
		providerRegistry: provider.NewRegistry(),
	}
	s.connector = newUpstreamConnector(s.connectUntilRegistered)
	return s
}

func mustStore(cfg *config.Config, configDir string, logger *slog.Logger, clock clock.Clock) *response.Store {
	storeCfg := response.StoreConfigFrom(cfg, configDir)
	storeCfg.Clock = clock
	store, err := response.NewStore(storeCfg)
	if err == nil {
		return store
	}
	logger.Error("failed to create response store, falling back to tmp", "err", err)
	storeCfg.Dir = filepath.Join(os.TempDir(), "mini-responses")
	store, err = response.NewStore(storeCfg)
	if err != nil {
		panic("failed to create fallback response store: " + err.Error())
	}
	return store
}

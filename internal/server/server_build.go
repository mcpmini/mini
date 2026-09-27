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
)

func New(cfg *config.Config, logger *slog.Logger, opts ...ServerOption) *Server {
	return NewWithConfigDir(cfg, config.DefaultConfigDir(), logger, opts...)
}

func NewWithConfigDir(cfg *config.Config, configDir string, logger *slog.Logger, opts ...ServerOption) *Server {
	load := config.LoadProjections(configDir)
	for _, se := range load.SourceErrors {
		logger.Warn("projection load: source error", "path", se.Path, "err", se.Err)
	}
	for name, err := range load.Skipped {
		logger.Warn("projections not loaded for server", "server", name, "err", err)
	}
	s := newServer(cfg, configDir, load.Projections, logger)
	for _, o := range opts {
		o(s)
	}
	store := mustStore(cfg, configDir, logger, s.clock)
	s.store = store
	s.envelope = response.NewBuilder(store)
	s.sessions = newSessionStore(s.clock)
	return s
}

func newServer(cfg *config.Config, configDir string, projections map[string]map[string]*config.ProjectionConfig, logger *slog.Logger) *Server {
	return &Server{
		cfg:              cfg,
		configDir:        configDir,
		reg:              registry.New(),
		upstreams:        make(map[string]*upstreamServer),
		removeGen:        make(map[string]uint64),
		projections:      projections,
		projDefaults:     projection.DefaultsFrom(cfg),
		toolSchemas:      compactToolSchemas(),
		authFlows:        make(map[string]*authFlowState),
		logger:           logger,
		clock:            clock.System(),
		providerRegistry: provider.NewRegistry(),
	}
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

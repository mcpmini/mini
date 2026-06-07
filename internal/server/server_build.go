package server

import (
	"log/slog"
	"os"
	"path/filepath"

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
	store := mustStore(cfg, logger)
	projections, err := loadServerProjections(configDir)
	if err != nil {
		logger.Warn("failed to load projections", "err", err)
	}
	s := newServer(cfg, configDir, store, projections, logger)
	for _, o := range opts {
		o(s)
	}
	return s
}

func newServer(cfg *config.Config, configDir string, store *response.Store, projections map[string]map[string]*config.ProjectionConfig, logger *slog.Logger) *Server {
	return &Server{
		cfg:          cfg,
		configDir:    configDir,
		reg:          registry.New(),
		upstreams:    make(map[string]*upstreamServer),
		removeGen:    make(map[string]uint64),
		projections:  projections,
		envelope:     response.NewBuilder(store, cfg.InlineThreshold),
		store:        store,
		projDefaults: projection.DefaultsFrom(cfg),
		toolSchemas:  proxyToolSchemas(),
		sessions:     newSessionStore(),
		authFlows: make(map[string]*authFlowState),
		logger:    logger,
		clock:        clock.System(),
	}
}

func loadServerProjections(configDir string) (map[string]map[string]*config.ProjectionConfig, error) {
	_, servers, err := config.Load(configDir)
	if err != nil {
		return make(map[string]map[string]*config.ProjectionConfig), err
	}
	out := make(map[string]map[string]*config.ProjectionConfig)
	for _, sc := range servers {
		if sc.Projections != nil {
			out[sc.Name] = sc.Projections
		}
	}
	return out, nil
}

func mustStore(cfg *config.Config, logger *slog.Logger) *response.Store {
	storeCfg := response.StoreConfigFrom(cfg)
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

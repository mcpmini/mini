package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/time/rate"

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
		projections:  projections,
		envelope:     response.NewBuilder(store, cfg.InlineThreshold),
		store:        store,
		projDefaults: newProjDefaults(cfg),
		toolSchemas:  proxyToolSchemas(),
		sessions:     newSessionStore(),
		authFlows:    make(map[string]*authFlowState),
		rateLimiters: make(map[string]rateLimiterEntry),
		rateLimit:    cfgRateLimit(cfg),
		logger:       logger,
		clock:        clock.System(),
	}
}

func cfgRateLimit(cfg *config.Config) rate.Limit {
	return rate.Limit(cfg.HTTPRateLimit)
}

func newProjDefaults(cfg *config.Config) *projection.Defaults {
	return &projection.Defaults{
		StringLimit:        cfg.DefaultStringLimit,
		DepthLimit:         cfg.DefaultDepthLimit,
		ContentFields:      cfg.ContentFields,
		AutoStripThreshold: cfg.AutoStripThreshold,
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
	storeCfg := buildStoreConfig(cfg)
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

func buildStoreConfig(cfg *config.Config) response.StoreConfig {
	return response.StoreConfig{
		Dir:             responseDir(cfg),
		TTL:             parseOrDefaultDuration(cfg.ResponseTTL, 168*time.Hour),
		BudgetMB:        cfg.ResponseDiskBudgetMB,
		CleanupInterval: time.Hour,
	}
}

func responseDir(cfg *config.Config) string {
	if cfg.ResponseDir != "" {
		return cfg.ResponseDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mini", "responses")
}

func parseOrDefaultDuration(spec string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(spec)
	if err != nil {
		return fallback
	}
	return d
}

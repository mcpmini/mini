package response

import (
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func StoreConfigFrom(cfg *config.Config, configDir string) StoreConfig {
	return StoreConfig{
		Dir:             responseDir(cfg, configDir),
		TTL:             parseOrDefaultDuration(cfg.ResponseTTL, time.Hour),
		BudgetMB:        cfg.ResponseDiskBudgetMB,
		CleanupInterval: time.Hour,
	}
}

func responseDir(cfg *config.Config, configDir string) string {
	if cfg.ResponseDir != "" {
		return cfg.ResponseDir
	}
	return filepath.Join(configDir, "internal", "responses")
}

func parseOrDefaultDuration(spec string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(spec)
	if err != nil {
		return fallback
	}
	return d
}

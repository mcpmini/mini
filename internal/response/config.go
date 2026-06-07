package response

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func StoreConfigFrom(cfg *config.Config) StoreConfig {
	return StoreConfig{
		Dir:             responseDir(cfg),
		TTL:             parseOrDefaultDuration(cfg.ResponseTTL, time.Hour),
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

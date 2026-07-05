package forge

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const denoCacheSweepAge = 7 * 24 * time.Hour

// SweepStaleDirs removes forge package cache dirs whose declared set has not
// been used in a while. The age gate keeps a sweep from deleting caches of
// runs in flight in another mini process: cache dirs are re-touched on every
// use.
func SweepStaleDirs(logger *slog.Logger) {
	sweepStale(logger, "forge-deno-", denoCacheSweepAge)
}

func sweepStale(logger *slog.Logger, prefix string, maxAge time.Duration) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		removeIfStale(logger, e, prefix, maxAge)
	}
}

func removeIfStale(logger *slog.Logger, e os.DirEntry, prefix string, maxAge time.Duration) {
	if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
		return
	}
	info, err := e.Info()
	// Real time because the age compares against filesystem mtimes.
	if err != nil || time.Since(info.ModTime()) < maxAge { //nolint:clocklint
		return
	}
	path := filepath.Join(os.TempDir(), e.Name())
	if err := os.RemoveAll(path); err == nil {
		logger.Info("swept stale forge dir", "dir", path)
	}
}

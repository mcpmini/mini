package ops

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

// PurgeExpiredResponses removes response files older than the configured TTL.
// Returns the number of files removed and bytes freed.
func PurgeExpiredResponses(configDir string) (removed int, freed int64, err error) {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		return 0, 0, err
	}
	dir, ttl := resolveResponseDir(cfg, configDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	removed, freed = purgeExpired(dir, entries, time.Now().Add(-ttl))
	return removed, freed, nil
}

func resolveResponseDir(cfg *config.Config, configDir string) (string, time.Duration) {
	dir := cfg.ResponseDir
	if dir == "" {
		dir = filepath.Join(configDir, "responses")
	}
	ttl, err := time.ParseDuration(cfg.ResponseTTL)
	if err != nil {
		ttl = time.Hour
	}
	return dir, ttl
}

func purgeExpired(dir string, entries []os.DirEntry, cutoff time.Time) (removed int, freed int64) {
	for _, e := range entries {
		if shouldSkipCleanupEntry(e) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		freed += purgeEntry(filepath.Join(dir, e.Name()), info.Size())
		removed++
	}
	return removed, freed
}

func purgeEntry(path string, size int64) int64 {
	os.Remove(path)
	return size
}

func shouldSkipCleanupEntry(e os.DirEntry) bool {
	return e.IsDir()
}

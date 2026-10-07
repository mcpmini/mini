package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

// PurgeExpiredResponses removes response files older than the configured TTL.
// Returns the number of files removed and bytes freed.
func PurgeExpiredResponses(configDir string, now time.Time) (removed int, freed int64, err error) {
	cfg, err := config.LoadMain(configDir)
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
	return purgeExpired(dir, entries, now.Add(-ttl))
}

func resolveResponseDir(cfg *config.Config, configDir string) (string, time.Duration) {
	dir := cfg.ResponseDir
	if dir == "" {
		dir = filepath.Join(configDir, "internal", "responses")
	}
	ttl, err := time.ParseDuration(cfg.ResponseTTL)
	if err != nil {
		ttl = time.Hour
	}
	return dir, ttl
}

func purgeExpired(dir string, entries []os.DirEntry, cutoff time.Time) (removed int, freed int64, err error) {
	var errs []error
	for _, e := range entries {
		if shouldSkipCleanupEntry(e) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		entryFreed, entryRemoved, err := purgeEntry(filepath.Join(dir, e.Name()), info.Size())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if entryRemoved {
			freed += entryFreed
			removed++
		}
	}
	return removed, freed, errors.Join(errs...)
}

func purgeEntry(path string, size int64) (freed int64, removed bool, err error) {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return 0, false, fmt.Errorf("remove response %q: %w", filepath.Base(path), err)
	}
	return size, true, nil
}

func shouldSkipCleanupEntry(e os.DirEntry) bool {
	return e.IsDir()
}

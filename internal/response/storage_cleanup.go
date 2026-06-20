package response

import (
	"log/slog"
	"os"
	"time"
)

func (s *Store) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.done:
			return
		}
	}
}

func (s *Store) evictExpired() {
	s.mu.Lock()
	kept, toRemove := partitionByExpiry(s.files)
	for _, f := range toRemove {
		s.usedBytes -= f.size
	}
	s.files = kept
	s.mu.Unlock()
	removeFiles(toRemove)
}

func partitionByExpiry(files []storedFile) (kept, expired []storedFile) {
	now := time.Now()
	kept = files[:0]
	for _, f := range files {
		if f.expires.After(now) {
			kept = append(kept, f)
		} else {
			expired = append(expired, f)
		}
	}
	return
}

// evictOvershoot removes oldest files until usedBytes is within the budget.
// Must be called with s.mu held. Returns the removed entries so the caller can
// delete the files after releasing the lock (file I/O must not run under the mutex).
// Keeps at least one file (the one just written) even if the budget is tight.
func (s *Store) evictOvershoot() []storedFile {
	if s.budgetBytes == 0 || s.usedBytes <= s.budgetBytes {
		return nil
	}
	var out []storedFile
	for s.usedBytes > s.budgetBytes && len(s.files) > 1 {
		out = append(out, s.files[0])
		s.usedBytes -= s.files[0].size
		s.files = s.files[1:]
	}
	return out
}

func removeFiles(files []storedFile) {
	for _, f := range files {
		warnRemoveErr(os.Remove(f.path))
	}
}

func warnRemoveErr(err error) {
	if err != nil && !os.IsNotExist(err) {
		slog.Default().Warn("response store: remove file failed", "err", err)
	}
}

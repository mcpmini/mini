package response

import (
	"log/slog"
	"os"
	"time"
)

func (s *Store) cleanupLoop(interval time.Duration) {
	for {
		t := s.clk.NewTimer(interval)
		select {
		case <-t.C():
			s.evictExpired()
		case <-s.done:
			t.Stop()
			return
		}
	}
}

func (s *Store) evictExpired() {
	s.mu.Lock()
	kept, toRemove := partitionByExpiry(s.files, s.clk.Now())
	for _, f := range toRemove {
		s.usedBytes -= f.size
	}
	s.files = kept
	s.mu.Unlock()
	s.restoreRemoveFailed(toRemove)
}

func partitionByExpiry(files []storedFile, now time.Time) (kept, expired []storedFile) {
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

func (s *Store) restoreRemoveFailed(files []storedFile) {
	failed := collectRemoveFailed(files)
	if len(failed) == 0 {
		return
	}
	s.mu.Lock()
	for _, f := range failed {
		s.usedBytes += f.size
	}
	s.files = append(failed, s.files...)
	s.mu.Unlock()
}

func collectRemoveFailed(files []storedFile) []storedFile {
	var failed []storedFile
	for _, f := range files {
		if warnRemoveErr(os.Remove(f.path)) {
			failed = append(failed, f)
		}
	}
	return failed
}

func warnRemoveErr(err error) bool {
	if err == nil || os.IsNotExist(err) {
		return false
	}
	slog.Default().Warn("response store: remove file failed", "err", err)
	return true
}

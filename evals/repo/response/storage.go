// Package response provides response file storage for the webapp API cache.
package response

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store writes and reads cached API response files.
type Store struct {
	dir        string
	budgetBytes int64
}

func NewStore(dir string, budgetMB int) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return &Store{dir: dir, budgetBytes: int64(budgetMB) * 1024 * 1024}, nil
}

// WritePair writes the summary and full raw response for a cache entry.
func (s *Store) WritePair(id string, summary, raw []byte) error {
	if err := s.checkBudget(int64(len(summary) + len(raw))); err != nil {
		return err
	}
	summaryPath := filepath.Join(s.dir, id+".json")
	rawPath := filepath.Join(s.dir, id+".raw.json")
	if err := os.WriteFile(summaryPath, summary, 0600); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if err := os.WriteFile(rawPath, raw, 0600); err != nil {
		return fmt.Errorf("write raw: %w", err)
	}
	return nil
}

func (s *Store) checkBudget(needed int64) error {
	if s.budgetBytes <= 0 {
		return nil
	}
	_, used := s.Stats()
	if used+needed > s.budgetBytes {
		return fmt.Errorf("disk budget exceeded: %.1fMB used of %.1fMB",
			float64(used)/1e6, float64(s.budgetBytes)/1e6)
	}
	return nil
}

// Stats returns the file count and total bytes in the store.
func (s *Store) Stats() (files int, usedBytes int64) {
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			files++
			usedBytes += info.Size()
		}
	}
	return
}

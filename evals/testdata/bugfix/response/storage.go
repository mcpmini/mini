package response

import (
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	dir         string
	budgetBytes int64
}

// WritePair writes two files for a cache entry.
func (s *Store) WritePair(id string, summary, raw []byte) error {
	if err := os.WriteFile(filepath.Join(s.dir, id+".json"), summary, 0600); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, id+".raw.json"), raw, 0600)
}

func (s *Store) usedBytes() int64 {
	entries, _ := os.ReadDir(s.dir)
	var n int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}

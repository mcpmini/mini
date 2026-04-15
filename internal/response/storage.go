package response

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Store struct {
	dir         string
	ttl         time.Duration
	budgetBytes int64
	usedBytes   int64
	mu          sync.Mutex
	closeOnce   sync.Once
	files       []storedFile
	done        chan struct{}
}

type storedFile struct {
	path    string
	rawPath string // paired .raw.json file, empty if none
	size    int64
	expires time.Time
}

type StoreConfig struct {
	Dir             string
	TTL             time.Duration
	BudgetMB        int
	CleanupInterval time.Duration
}

func NewStore(cfg StoreConfig) (*Store, error) {
	if err := os.MkdirAll(cfg.Dir, 0700); err != nil {
		return nil, fmt.Errorf("create response dir: %w", err)
	}
	// Enforce 0700 even if dir already existed with looser permissions
	// (e.g. response_dir overridden to a world-readable location like /tmp).
	if err := os.Chmod(cfg.Dir, 0700); err != nil {
		return nil, fmt.Errorf("secure response dir: %w", err)
	}
	s := &Store{
		dir:         cfg.Dir,
		ttl:         cfg.TTL,
		budgetBytes: int64(cfg.BudgetMB) * 1024 * 1024,
		done:        make(chan struct{}),
	}
	s.loadExisting()
	go s.cleanupLoop(cfg.CleanupInterval)
	return s, nil
}

func (s *Store) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

func (s *Store) Stats() (fileCount int, usedBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.files), s.usedBytes
}

func (s *Store) Dir() string { return s.dir }

func uniqueBase(base string, i int) string {
	if i == 0 {
		return base
	}
	return fmt.Sprintf("%s_%04d", base, i)
}

const tsLayout = "20060102150405"

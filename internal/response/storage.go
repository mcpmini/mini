package response

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type Store struct {
	dir         string
	ttl         time.Duration
	budgetBytes int64
	usedBytes   int64
	clk         clock.Clock
	mu          sync.Mutex
	closeOnce   sync.Once
	files       []storedFile
	done        chan struct{}
}

type storedFile struct {
	path    string
	size    int64
	expires time.Time
}

type StoreConfig struct {
	Dir             string
	TTL             time.Duration
	BudgetMB        int
	CleanupInterval time.Duration
	Clock           clock.Clock
}

func NewStore(cfg StoreConfig) (*Store, error) {
	if err := secureDir(cfg.Dir); err != nil {
		return nil, err
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System()
	}
	s := &Store{
		dir:         cfg.Dir,
		ttl:         cfg.TTL,
		budgetBytes: int64(cfg.BudgetMB) * 1024 * 1024,
		clk:         clk,
		done:        make(chan struct{}),
	}
	s.loadExisting()
	go s.cleanupLoop(cfg.CleanupInterval)
	return s, nil
}

// secureDir creates dir and enforces 0700 even if it already existed with looser
// permissions (e.g. response_dir overridden to a world-readable location like /tmp).
func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create response dir: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("secure response dir: %w", err)
	}
	return nil
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


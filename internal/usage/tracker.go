package usage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type ToolStats struct {
	Server      string `json:"server"`
	Tool        string `json:"tool"`
	Calls       int64  `json:"calls"`
	Errors      int64  `json:"errors"`
	TokensSaved int64  `json:"tokens_saved"`
}

type toolKey struct{ server, tool string }

type fileData struct {
	Version   int         `json:"version"`
	UpdatedAt time.Time   `json:"updated_at"`
	Tools     []ToolStats `json:"tools"`
}

// Tracker is safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	path    string
	entries map[toolKey]*ToolStats
}

func New(path string) *Tracker {
	return &Tracker{path: path, entries: make(map[toolKey]*ToolStats)}
}

func (t *Tracker) Record(server, tool string, tokensSaved int64, errored bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.getOrCreateLocked(server, tool)
	e.Calls++
	if errored {
		e.Errors++
	}
	e.TokensSaved += tokensSaved
}

func (t *Tracker) getOrCreateLocked(server, tool string) *ToolStats {
	k := toolKey{server, tool}
	if e, ok := t.entries[k]; ok {
		return e
	}
	e := &ToolStats{Server: server, Tool: tool}
	t.entries[k] = e
	return e
}

// Load is a no-op if the file does not exist yet.
func (t *Tracker) Load() error {
	b, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f fileData
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range f.Tools {
		s := s
		t.entries[toolKey{s.Server, s.Tool}] = &s
	}
	return nil
}

func (t *Tracker) Flush() error {
	t.mu.Lock()
	tools := t.snapshotLocked()
	t.mu.Unlock()
	return persistFile(t.path, fileData{Version: 1, UpdatedAt: time.Now().UTC(), Tools: tools})
}

func (t *Tracker) snapshotLocked() []ToolStats {
	out := make([]ToolStats, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}

func persistFile(path string, f fileData) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TopTools returns all tools when n <= 0.
func (t *Tracker) TopTools(n int) []ToolStats {
	t.mu.Lock()
	all := t.snapshotLocked()
	t.mu.Unlock()
	if n > 0 && len(all) > n {
		return all[:n]
	}
	return all
}

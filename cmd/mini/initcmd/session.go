package initcmd

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/server"
)

type SessionParams struct {
	ConfigDir string
	Clock     clock.Clock
	Probe     func(ctx context.Context, configDir string, sc config.ServerConfig) error
}

// Session writes the servers picked in this run and checks the new ones for OAuth. Only
// servers it wrote are ever removed; servers configured before the run are never touched.
// Sync, Written and Close belong to one goroutine; Checking and Changed are safe from any.
type Session struct {
	p       SessionParams
	written map[string]writtenServer
	checks  checkRun
	changed chan struct{}

	mu       sync.Mutex
	checking map[string]bool
	checked  map[string]bool
}

// encoded is the baseline for spotting a changed config; the caller can't alter it through
// maps or pointers it shares with config.
type writtenServer struct {
	config  config.ServerConfig
	encoded []byte
}

func newWrittenServer(sc config.ServerConfig) writtenServer {
	encoded, _ := yaml.Marshal(sc) //nolint:errcheck // AddServer just marshaled the same config without error
	return writtenServer{config: sc, encoded: encoded}
}

func (w writtenServer) sameAs(sc config.ServerConfig) bool {
	encoded, err := yaml.Marshal(sc)
	return err == nil && bytes.Equal(encoded, w.encoded)
}

type checkRun struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type SyncResult struct {
	Added   []ops.AddedServer
	Removed []string
	Failed  []ServerError
}

type ServerError struct {
	Name string
	Err  error
}

func NewSession(p SessionParams) *Session {
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	if p.Probe == nil {
		p.Probe = server.ProbeServer
	}
	return &Session{
		p: p, written: map[string]writtenServer{}, changed: make(chan struct{}, 1),
		checking: map[string]bool{}, checked: map[string]bool{},
	}
}

// Sync makes the servers written in this run match want, then checks the written HTTP servers
// that may need OAuth. A server whose config changed is removed and written again; a replacement
// mini would reject leaves the previous config in place and is reported as failed.
func (s *Session) Sync(want []config.ServerConfig) SyncResult {
	s.stopChecks()
	var result SyncResult
	s.removeUnwanted(want, &result)
	s.addMissing(want, &result)
	s.startChecks()
	return result
}

func (s *Session) removeUnwanted(want []config.ServerConfig, result *SyncResult) {
	for _, name := range slices.Sorted(maps.Keys(s.written)) {
		i := slices.IndexFunc(want, func(sc config.ServerConfig) bool { return sc.Name == name })
		if i >= 0 && s.written[name].sameAs(want[i]) {
			continue
		}
		if i >= 0 {
			if err := ops.ValidateServer(s.p.ConfigDir, want[i]); err != nil {
				result.Failed = append(result.Failed, ServerError{Name: name, Err: err})
				continue
			}
		}
		if err := ops.RemoveServer(s.p.ConfigDir, name); err != nil {
			result.Failed = append(result.Failed, ServerError{Name: name, Err: err})
			continue
		}
		delete(s.written, name)
		delete(s.checked, name)
		result.Removed = append(result.Removed, name)
	}
}

func (s *Session) addMissing(want []config.ServerConfig, result *SyncResult) {
	for _, sc := range want {
		if _, ok := s.written[sc.Name]; ok {
			continue
		}
		added, err := ops.AddServer(s.p.ConfigDir, sc)
		if err != nil {
			result.Failed = append(result.Failed, ServerError{Name: sc.Name, Err: err})
			continue
		}
		s.written[sc.Name] = newWrittenServer(sc)
		result.Added = append(result.Added, added)
	}
}

func (s *Session) Written() []config.ServerConfig {
	var written []config.ServerConfig
	for _, name := range slices.Sorted(maps.Keys(s.written)) {
		written = append(written, s.written[name].config)
	}
	return written
}

// Checking reports whether name's OAuth check is still running; until it finishes, whether the
// server needs a login isn't known.
func (s *Session) Checking(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checking[name]
}

// Changed receives after a check finishes; a receive may cover several.
func (s *Session) Changed() <-chan struct{} {
	return s.changed
}

// Close cancels the running checks and waits for them, so nothing is written after it returns.
func (s *Session) Close() {
	s.stopChecks()
}

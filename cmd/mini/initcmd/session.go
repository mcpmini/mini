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
	Probe     probeFunc
}

// Session writes this run's servers and removes only servers it wrote.
// Sync, Written, Unchecked, WaitChecks and Close belong to one goroutine; Running and Changed are
// safe from any.
type Session struct {
	p       SessionParams
	written map[string]writtenServer
	checks  checkRun
	changed chan struct{}

	mu       sync.Mutex
	checking map[string]bool
	checked  map[string]bool
}

type writtenServer struct {
	// Bytes, not the config: the caller still shares its maps and pointers.
	encoded []byte
}

func newWrittenServer(sc config.ServerConfig) writtenServer {
	encoded, _ := yaml.Marshal(sc) //nolint:errcheck // AddServer just encoded the same config without error
	return writtenServer{encoded: encoded}
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
	Added   []string
	Removed []string
	Failed  []ServerError
}

func NewSession(p SessionParams) *Session {
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	if p.Probe == nil {
		p.Probe = server.ProbeServer
	}
	return &Session{
		p:        p,
		written:  map[string]writtenServer{},
		changed:  make(chan struct{}, 1),
		checking: map[string]bool{},
		checked:  map[string]bool{},
	}
}

// Sync makes this run's servers match want. A changed config that mini would reject leaves the
// previous one in place, reported as failed.
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
		s.mu.Lock()
		delete(s.checked, name)
		s.mu.Unlock()
		result.Removed = append(result.Removed, name)
	}
}

func (s *Session) addMissing(want []config.ServerConfig, result *SyncResult) {
	for _, sc := range want {
		if _, ok := s.written[sc.Name]; ok {
			continue
		}
		if _, err := ops.AddServer(s.p.ConfigDir, sc); err != nil {
			result.Failed = append(result.Failed, ServerError{Name: sc.Name, Err: err})
			continue
		}
		s.written[sc.Name] = newWrittenServer(sc)
		result.Added = append(result.Added, sc.Name)
	}
}

func (s *Session) Written() []string {
	return slices.Sorted(maps.Keys(s.written))
}

// Changed receives after a check starts or finishes; a receive may cover several.
func (s *Session) Changed() <-chan struct{} {
	return s.changed
}

func (s *Session) Running() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.checking)
}

// Unchecked names the written servers that may need OAuth and whose check never finished, as when
// Close cancelled it.
func (s *Session) Unchecked() []string {
	var names []string
	for _, sc := range s.checkTargets() {
		names = append(names, sc.Name)
	}
	return names
}

func (s *Session) WaitChecks() {
	s.checks.wg.Wait()
}

// Close cancels the running checks and waits, so nothing is written after it returns.
func (s *Session) Close() {
	s.stopChecks()
}

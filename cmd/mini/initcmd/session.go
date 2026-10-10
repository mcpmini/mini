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
)

type sessionParams struct {
	ConfigDir string
	Clock     clock.Clock
	Probe     probeFunc
}

// Only servers this run wrote are ever removed; servers the user had before are never touched.
// Sync, Written, WaitChecks and Close belong to one goroutine; Running and Changed are
// safe from any.
type session struct {
	p       sessionParams
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

type syncResult struct {
	Added   []string
	Removed []string
	Failed  []ServerError
}

func newSession(p sessionParams) *session {
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	return &session{
		p:        p,
		written:  map[string]writtenServer{},
		changed:  make(chan struct{}, 1),
		checking: map[string]bool{},
		checked:  map[string]bool{},
	}
}

// Sync makes this run's servers match want. A changed config that mini would reject leaves the
// previous one in place, reported as failed.
func (s *session) Sync(want []config.ServerConfig) syncResult {
	s.stopChecks()
	var result syncResult
	s.removeUnwanted(want, &result)
	s.addMissing(want, &result)
	s.startChecks()
	return result
}

func (s *session) removeUnwanted(want []config.ServerConfig, result *syncResult) {
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

func (s *session) addMissing(want []config.ServerConfig, result *syncResult) {
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

func (s *session) Written() []string {
	return slices.Sorted(maps.Keys(s.written))
}

func (s *session) Changed() <-chan struct{} {
	return s.changed
}

func (s *session) Running() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.checking)
}

func (s *session) WaitChecks() {
	s.checks.wg.Wait()
}

// Close cancels the running checks and waits, so nothing is written after it returns.
func (s *session) Close() {
	s.stopChecks()
}

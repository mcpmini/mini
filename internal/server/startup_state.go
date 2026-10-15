package server

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

// Above the cold start measured in #322 (6 s) and well inside the 30 s request timeouts of Claude Code, Codex and opencode.
const startupHold = 10 * time.Second

type startupPhase string

const (
	phaseConnected  startupPhase = "connected"
	phaseConnecting startupPhase = "connecting"
	phaseDelayed    startupPhase = "delayed"
	phaseFailed     startupPhase = "failed"
)

//sumtype:decl
type startupStatus interface {
	startupStatus()
}

//sumtype:decl
type startupFailure interface {
	startupStatus
	startupFailure()
	logMessage() string
}

type (
	connectedStatus  struct{}
	connectingStatus struct{}
	delayedStatus    struct{}
	needsAuthStatus  struct{}
	needsEnvStatus   struct{ envVars []string }
	notTrustedStatus struct{}
)

func (connectedStatus) startupStatus()   {}
func (connectingStatus) startupStatus()  {}
func (delayedStatus) startupStatus()     {}
func (needsAuthStatus) startupStatus()   {}
func (needsEnvStatus) startupStatus()    {}
func (notTrustedStatus) startupStatus()  {}
func (needsAuthStatus) startupFailure()  {}
func (needsEnvStatus) startupFailure()   {}
func (notTrustedStatus) startupFailure() {}

func (needsAuthStatus) logMessage() string {
	return "upstream needs authorization, not retrying"
}

func (needsEnvStatus) logMessage() string {
	return "upstream needs an environment variable mini didn't start with, not retrying"
}

func (notTrustedStatus) logMessage() string {
	return "upstream not allowed to start, not retrying"
}

type startupTracker struct {
	startedAt  map[string]time.Time
	toolsReady map[string]bool // set after the registry has the tools; upstreams is set before
	failures   map[string]startupFailure
	changed    chan struct{}
}

func newStartupTracker() *startupTracker {
	return &startupTracker{
		startedAt:  make(map[string]time.Time),
		toolsReady: make(map[string]bool),
		failures:   make(map[string]startupFailure),
		changed:    make(chan struct{}),
	}
}

func (t *startupTracker) open(name string, now time.Time) {
	t.startedAt[name] = now
	t.broadcastChange()
}

func (t *startupTracker) markReady(name string) {
	t.toolsReady[name] = true
	t.broadcastChange()
}

func (t *startupTracker) recordFailure(name string, failure startupFailure) {
	t.failures[name] = failure
	t.broadcastChange()
}

func (t *startupTracker) forget(name string) {
	delete(t.startedAt, name)
	delete(t.toolsReady, name)
	delete(t.failures, name)
	t.broadcastChange()
}

func (t *startupTracker) broadcastChange() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func (t *startupTracker) ready(name string) bool { return t.toolsReady[name] }

func (t *startupTracker) windowEnd(name string) time.Time { return t.startedAt[name].Add(startupHold) }

func (t *startupTracker) state(name string, now time.Time) startupState {
	if t.toolsReady[name] {
		return startupState{status: connectedStatus{}}
	}
	if failure, ok := t.failures[name]; ok {
		return startupState{status: failure}
	}
	if now.Before(t.windowEnd(name)) {
		return startupState{status: connectingStatus{}}
	}
	return startupState{status: delayedStatus{}}
}

type startupState struct {
	status startupStatus
}

func (st startupState) phase() startupPhase {
	switch st.status.(type) {
	case connectedStatus:
		return phaseConnected
	case connectingStatus:
		return phaseConnecting
	case delayedStatus:
		return phaseDelayed
	case needsAuthStatus, needsEnvStatus, notTrustedStatus:
		return phaseFailed
	}
	return phaseFailed
}

func terminalStartupFailure(err error) (startupFailure, bool) {
	var unset *config.UnsetEnvError
	switch {
	case errors.Is(err, transport.ErrReauthRequired):
		return needsAuthStatus{}, true
	case errors.Is(err, invoke.ErrAgentCommandNotAllowed):
		return notTrustedStatus{}, true
	case errors.As(err, &unset) && unset != nil:
		return needsEnvStatus{envVars: unset.Names}, true
	}
	return nil, false
}

func (s *Server) openConnectWindow(name string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.startup.open(name, s.clock.Now())
}

func (s *Server) recordStartupFailure(in upstreamInstall, failure startupFailure) {
	loginSavedAt := s.loginSavedAt(in.cfg.Name)
	// A 401 may have made mini note the server uses OAuth, which every later load merges in. Recorded
	// with it, the next check doesn't take the note for an edit of the file.
	detected := in.cfg
	config.MergeDetectedOAuth(s.configDir, &detected)
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if s.removeGen[in.cfg.Name] != in.removeGen {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.startup.recordFailure(in.cfg.Name, failure)
	if record, ok := s.configServers[in.cfg.Name]; ok {
		record.loginSavedAt = loginSavedAt
		if record.settings.Auth == nil {
			record.settings.Auth = detected.Auth
		}
		s.configServers[in.cfg.Name] = record
	}
}

func (s *Server) markToolsReady(name string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.startup.markReady(name)
}

type startupReport struct {
	starting    []string
	unavailable map[string]any
}

func (s *Server) startupReportLocked() startupReport {
	now := s.clock.Now()
	report := startupReport{starting: []string{}, unavailable: map[string]any{}}
	for name := range s.configServers {
		state := s.startup.state(name, now)
		switch state.status.(type) {
		case connectingStatus:
			report.starting = append(report.starting, name)
		case delayedStatus, needsAuthStatus, needsEnvStatus, notTrustedStatus:
			report.unavailable[name] = map[string]any{"state": state.phase(), "reason": state.reason(name)}
		case connectedStatus:
			continue
		}
	}
	slices.Sort(report.starting)
	return report
}

// Agents read these, so they never include the upstream error: it can hold credentials.
func (st startupState) reason(name string) string {
	switch status := st.status.(type) {
	case connectedStatus:
		return ""
	case connectingStatus:
		return fmt.Sprintf(
			"server %q is still connecting; its tools appear when it's ready. Try again in a few seconds.", name)
	case delayedStatus:
		return fmt.Sprintf(
			"server %q hasn't connected yet; mini keeps trying in the background. "+
				`If this persists, ask the user to run "mini status".`, name)
	case needsAuthStatus:
		return status.reason(name)
	case needsEnvStatus:
		return status.reason(name)
	case notTrustedStatus:
		return status.reason(name)
	}
	return fmt.Sprintf(`server %q could not start; ask the user to run "mini status".`, name)
}

func (status needsEnvStatus) reason(name string) string {
	return fmt.Sprintf(
		"server %q needs %s, which the running mini was started without. "+
			"Ask the user to write what's missing into its server file, which mini applies within seconds, "+
			"or to set it where mini runs and restart mini.",
		name, envVarList(status.envVars))
}

func envVarList(names []string) string {
	if len(names) == 1 {
		return "the environment variable " + names[0]
	}
	return "the environment variables " + strings.Join(names, ", ")
}

func (needsAuthStatus) reason(name string) string {
	return fmt.Sprintf(
		`server %q needs authorization: call config with action "start_auth" and server %q, `+
			"then finish the login in the browser.", name, name)
}

func (notTrustedStatus) reason(name string) string {
	return fmt.Sprintf(
		"server %q runs a command an agent added, so mini won't start it until a person trusts it. "+
			"Ask the user to delete agent_added from its server file; mini starts it within seconds of the save.", name)
}

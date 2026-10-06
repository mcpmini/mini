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

// Long enough for the servers measured on a cold start (6 s), well inside clients' 30 s request timeouts.
const startupHold = 10 * time.Second

type startupPhase string

const (
	phaseConnected  startupPhase = "connected"
	phaseConnecting startupPhase = "connecting"
	phaseDelayed    startupPhase = "delayed"
	phaseFailed     startupPhase = "failed"
)

type failureKind int

const (
	failureNeedsAuth failureKind = iota + 1
	failureNeedsEnv
	failureNotTrusted
)

type startupFailure struct {
	kind    failureKind
	envVars []string
}

type startupState struct {
	phase   startupPhase
	failure startupFailure
}

func terminalStartupFailure(err error) (startupFailure, bool) {
	var unset *config.UnsetEnvError
	switch {
	case errors.Is(err, transport.ErrReauthRequired):
		return startupFailure{kind: failureNeedsAuth}, true
	case errors.Is(err, invoke.ErrAgentCommandNotAllowed):
		return startupFailure{kind: failureNotTrusted}, true
	case errors.As(err, &unset):
		return startupFailure{kind: failureNeedsEnv, envVars: unset.Names}, true
	}
	return startupFailure{}, false
}

func (f startupFailure) logMessage() string {
	switch f.kind {
	case failureNeedsAuth:
		return "upstream needs authorization, not retrying"
	case failureNotTrusted:
		return "upstream not allowed to start, not retrying"
	default:
		return "upstream needs an environment variable mini didn't start with, not retrying"
	}
}

func (s *Server) openConnectWindow(name string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.connectStartedAt[name] = s.clock.Now()
}

func (s *Server) recordStartupFailure(in upstreamInstall, failure startupFailure) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if s.removeGen[in.cfg.Name] != in.removeGen {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.startupFailures[in.cfg.Name] = failure
}

func (s *Server) markToolsReady(name string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.toolsReady[name] = true
}

func (s *Server) forgetStartupLocked(name string) {
	delete(s.connectStartedAt, name)
	delete(s.toolsReady, name)
	delete(s.startupFailures, name)
}

func (s *Server) startupStateLocked(name string, now time.Time) startupState {
	if s.toolsReady[name] {
		return startupState{phase: phaseConnected}
	}
	if failure, ok := s.startupFailures[name]; ok {
		return startupState{phase: phaseFailed, failure: failure}
	}
	if now.Before(s.connectStartedAt[name].Add(startupHold)) {
		return startupState{phase: phaseConnecting}
	}
	return startupState{phase: phaseDelayed}
}

type startupReport struct {
	starting    []string
	unavailable map[string]any
}

func (s *Server) startupReportLocked() startupReport {
	now := s.clock.Now()
	report := startupReport{starting: []string{}, unavailable: map[string]any{}}
	for name := range s.configServers {
		state := s.startupStateLocked(name, now)
		switch state.phase {
		case phaseConnecting:
			report.starting = append(report.starting, name)
		case phaseDelayed, phaseFailed:
			report.unavailable[name] = map[string]any{"state": state.phase, "reason": state.reason(name)}
		}
	}
	slices.Sort(report.starting)
	return report
}

// Agents read these, so they never include the upstream error: it can hold credentials.
func (st startupState) reason(name string) string {
	switch st.phase {
	case phaseConnecting:
		return fmt.Sprintf(
			"server %q is still connecting; its tools appear when it's ready. Try again in a few seconds.", name)
	case phaseDelayed:
		return fmt.Sprintf(
			"server %q hasn't connected yet; mini keeps trying in the background. "+
				`If this persists, ask the user to run "mini status".`, name)
	}
	return st.failure.reason(name)
}

func (f startupFailure) reason(name string) string {
	switch f.kind {
	case failureNeedsAuth:
		return fmt.Sprintf(
			`server %q needs authorization: call config with action "start_auth" and server %q, `+
				"then finish the login in the browser.", name, name)
	case failureNeedsEnv:
		return fmt.Sprintf(
			"server %q needs %s, which the running mini was started without. "+
				"Ask the user to provide what's missing; mini picks it up only when it starts again.",
			name, envVarList(f.envVars))
	default:
		return fmt.Sprintf(
			"server %q runs a command an agent added, so mini won't start it until a person trusts it. "+
				"Ask the user to delete agent_added from its server file; mini picks the change up only when it starts again.",
			name)
	}
}

func envVarList(names []string) string {
	if len(names) == 1 {
		return "the environment variable " + names[0]
	}
	return "the environment variables " + strings.Join(names, ", ")
}

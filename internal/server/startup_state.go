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

func (u *upstreamServer) startupState(now time.Time) startupState {
	u.mu.RLock()
	defer u.mu.RUnlock()
	switch {
	case u.ready:
		return startupState{status: connectedStatus{}}
	case u.failure != nil:
		return startupState{status: u.failure}
	case now.Before(u.windowEnd()):
		return startupState{status: connectingStatus{}}
	}
	return startupState{status: delayedStatus{}}
}

func (u *upstreamServer) windowEnd() time.Time { return u.openedAt.Add(startupHold) }

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

type startupReport struct {
	starting    []string
	unavailable map[string]any
}

// Each server is classified from one read of its state, so one that finishes connecting meanwhile
// is never listed both as ready and as starting.
func (s *Server) classifyStartup(upstreams []*upstreamServer) (map[string]*upstreamServer, startupReport) {
	now := s.clock.Now()
	ready := make(map[string]*upstreamServer, len(upstreams))
	report := startupReport{starting: []string{}, unavailable: map[string]any{}}
	for _, u := range upstreams {
		state := u.startupState(now)
		if _, connected := state.status.(connectedStatus); connected {
			ready[u.cfg.Name] = u
			continue
		}
		if !u.fromConfig {
			continue
		}
		switch state.status.(type) {
		case connectingStatus:
			report.starting = append(report.starting, u.cfg.Name)
		case delayedStatus, needsAuthStatus, needsEnvStatus, notTrustedStatus:
			report.unavailable[u.cfg.Name] = map[string]any{"state": state.phase(), "reason": state.reason(u.cfg.Name)}
		case connectedStatus:
		}
	}
	slices.Sort(report.starting)
	return ready, report
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

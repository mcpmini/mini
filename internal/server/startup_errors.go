package server

import (
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

// A missing tool on a configured server that isn't connected reports the server's state instead of
// not_found, so the agent can tell "wait", "ask the user" and "doesn't exist" apart (#279).
type errServerNotReady struct{ env *response.Envelope }

func (e errServerNotReady) Error() string { return e.env.Message }

func (s *Server) lookupTool(server, tool string) (*registry.ToolEntry, error) {
	// State first: a server is marked ready only after its tools are registered, so a server that
	// becomes ready in between is found by the lookup instead of reported as not_found.
	if env, notReady := s.serverNotReadyError(server); notReady {
		return nil, errServerNotReady{env}
	}
	entry, err := s.reg.Lookup(toolFullName(server, tool))
	if err != nil {
		return nil, errLookup{err}
	}
	return entry, nil
}

func (s *Server) serverNotReadyError(server string) (*response.Envelope, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if !s.configServers[server] {
		return nil, false
	}
	state := s.startup.state(server, s.clock.Now())
	if state.phase() == phaseConnected {
		return nil, false
	}
	return response.BuildError(state.errorCode(), state.reason(server), state.retryable(), state.action()), true
}

func (st startupState) errorCode() string {
	switch st.status.(type) {
	case connectedStatus:
		return ""
	case connectingStatus:
		return "server_starting"
	case delayedStatus:
		return "server_delayed"
	case needsAuthStatus:
		return "server_needs_auth"
	case needsEnvStatus:
		return "server_needs_env"
	case notTrustedStatus:
		return "server_not_trusted"
	}
	return "server_failed"
}

func (st startupState) retryable() bool {
	switch st.status.(type) {
	case connectingStatus, delayedStatus:
		return true
	case connectedStatus, needsAuthStatus, needsEnvStatus, notTrustedStatus:
		return false
	}
	return false
}

func (st startupState) action() string {
	switch st.status.(type) {
	case needsAuthStatus:
		return "start_auth"
	case connectedStatus, connectingStatus, delayedStatus, needsEnvStatus, notTrustedStatus:
		return ""
	}
	return ""
}

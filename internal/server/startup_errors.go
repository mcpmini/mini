package server

import "github.com/mcpmini/mini/internal/response"

// A missing tool on a configured server that isn't connected gets the server's state instead of
// not_found, so the agent can tell "wait", "ask the user" and "doesn't exist" apart (#279).
func (s *Server) unavailableServerError(server string) (*response.Envelope, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if !s.configServers[server] {
		return nil, false
	}
	state := s.startup.state(server, s.clock.Now())
	if state.phase == phaseConnected {
		return nil, false
	}
	return response.BuildError(state.errorCode(), state.reason(server), state.retryable(), state.action()), true
}

func (st startupState) errorCode() string {
	switch st.phase {
	case phaseConnecting:
		return "server_starting"
	case phaseDelayed:
		return "server_unavailable"
	}
	switch st.failure.kind {
	case failureNeedsAuth:
		return "server_needs_auth"
	case failureNeedsEnv:
		return "server_needs_env"
	default:
		return "server_not_trusted"
	}
}

func (st startupState) retryable() bool { return st.phase != phaseFailed }

func (st startupState) action() string {
	if st.phase == phaseFailed && st.failure.kind == failureNeedsAuth {
		return "start_auth"
	}
	return ""
}

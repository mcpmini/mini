package server

import (
	"errors"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// add_server saves the agent's config, so it runs again on every start: drop whatever could
// carry the user's credentials or make the running server differ from the saved one.
func (s *Server) agentServerConfig(raw *config.ServerConfig) (config.ServerConfig, error) {
	if raw == nil {
		return config.ServerConfig{}, errors.New("config is required")
	}
	if err := validateServerName(raw.Name); err != nil {
		return config.ServerConfig{}, err
	}
	sc := *raw
	sc.Auth, sc.Headers, sc.Env = nil, nil, nil
	sc.Projections, sc.Enabled, sc.HandshakeTimeout = nil, nil, ""
	if err := s.checkAgentTransport(sc); err != nil {
		return config.ServerConfig{}, err
	}
	sc.AgentAdded = true
	return sc, nil
}

func (s *Server) checkAgentTransport(sc config.ServerConfig) error {
	if !sc.IsHTTPTransport() {
		if !s.cfg.DangerousAllowRuntimeStdio {
			return errors.New("only http/sse/streamable transports are allowed; set dangerous_allow_runtime_stdio: true to enable stdio")
		}
		return nil
	}
	if s.cfg.DangerousAllowPrivateURLs {
		return nil
	}
	return transport.ValidateURL(sc.URL)
}

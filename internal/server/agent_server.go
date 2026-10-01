package server

import (
	"errors"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// add_server saves what it accepts, so it runs on every start. Only the connection
// comes from the agent: everything else keeps mini's defaults, so an agent can't loosen
// credentials, permissions, projections or timeouts, including fields added later.
func (s *Server) agentServerConfig(raw *config.ServerConfig) (config.ServerConfig, error) {
	if raw == nil {
		return config.ServerConfig{}, errors.New("config is required")
	}
	if err := validateServerName(raw.Name); err != nil {
		return config.ServerConfig{}, err
	}
	sc := config.ServerConfig{Name: raw.Name, Transport: raw.Transport, URL: raw.URL, Command: raw.Command, Args: raw.Args, AgentAdded: true}
	if err := s.checkAgentTransport(sc); err != nil {
		return config.ServerConfig{}, err
	}
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

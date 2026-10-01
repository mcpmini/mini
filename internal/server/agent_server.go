package server

import (
	"errors"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// add_server saves an agent's config and runs it on every start, so nothing in it may carry credentials or reach the local network.
func (s *Server) agentServerConfig(raw *config.ServerConfig) (config.ServerConfig, error) {
	if raw == nil {
		return config.ServerConfig{}, errors.New("config is required")
	}
	if err := validateServerName(raw.Name); err != nil {
		return config.ServerConfig{}, err
	}
	sc := *raw
	sc.Auth, sc.Headers, sc.Env = nil, nil, nil
	sc.Projections, sc.Enabled = nil, nil
	if err := s.checkAgentTransport(sc); err != nil {
		return config.ServerConfig{}, err
	}
	sc.BlockPrivateIPs = true
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

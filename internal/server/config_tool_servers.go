package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) addServerFromAgent(ctx context.Context, raw *config.ServerConfig) (any, error) {
	sc, err := s.agentServerConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("add_server: %w", err)
	}
	unlock := s.serverNames.lock(sc.Name)
	defer unlock()
	if err := s.saveAndConnect(ctx, sc); err != nil {
		return nil, err
	}
	s.logger.Info("server added by the config tool", "server", sc.Name)
	return map[string]any{"ok": true, "server": sc.Name}, nil
}

func (s *Server) saveAndConnect(ctx context.Context, sc config.ServerConfig) error {
	if s.upstreams.get(sc.Name) != nil {
		return errAlreadyRunning(sc.Name)
	}
	if err := s.refuseConfiguredName(sc.Name); err != nil {
		return err
	}
	// Nothing runs under the name, but a token refresh or a login for its earlier server may: they
	// would save a token the new server then picks up.
	s.remove(sc.Name, retireRemoved)
	err := s.changeSavedFiles(func() error {
		_, err := ops.AddServer(s.configDir, sc)
		return err
	})
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		return errAlreadyConfigured(sc.Name)
	}
	if err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	if err := s.connectSaved(ctx, sc.Name); err != nil {
		// The failed connect may have acquired a provider for this name and URL.
		s.providerRegistry.Forget(sc.Name)
		return errors.Join(err, s.rollBackAdd(sc.Name))
	}
	return nil
}

func (s *Server) refuseConfiguredName(name string) error {
	exists, err := config.ServerFileExists(s.configDir, name)
	if err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	if exists {
		return errAlreadyConfigured(name)
	}
	return nil
}

func (s *Server) connectSaved(ctx context.Context, name string) error {
	saved, err := config.LoadServer(s.configDir, name)
	if err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	conn, toolList, err := s.openUpstream(ctx, saved)
	if err != nil {
		return err
	}
	return s.replaceConnected(s.newConfigServer(saved), connected{conn: conn, tools: toolList})
}

func (s *Server) rollBackAdd(name string) error {
	if err := s.removeSavedFiles(name); err != nil {
		return fmt.Errorf(
			"add_server: %s is still saved, so mini will keep trying to connect it; remove it with remove_server: %w",
			name,
			err,
		)
	}
	return nil
}

func (s *Server) removeSavedFiles(name string) error {
	return s.changeSavedFiles(func() error { return ops.RemoveServer(s.configDir, name) })
}

// changeSavedFiles holds persistMu so a set_projection can't interleave with the change. The caller
// holds the name's lock and has already stopped whatever ran under it.
func (s *Server) changeSavedFiles(change func() error) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return change()
}

func errAlreadyRunning(name string) error {
	return fmt.Errorf("add_server: %s is already running; remove it with remove_server first", name)
}

func errAlreadyConfigured(name string) error {
	return fmt.Errorf("add_server: %s is already configured; remove it with remove_server first", name)
}

func (s *Server) removeServerFromAgent(name string) (any, error) {
	if err := validateServerName(name); err != nil {
		return nil, fmt.Errorf("remove_server: %w", err)
	}
	unlock := s.serverNames.lock(name)
	defer unlock()
	// Stopped before the files go, so nothing it ran can write them back.
	s.remove(name, retireRemoved)
	if err := s.removeSavedFiles(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf(
			"remove_server: %s is still saved, so mini will connect it again; try remove_server again: %w",
			name,
			err,
		)
	}
	s.logger.Info("server removed by the config tool", "server", name)
	return map[string]any{"ok": true, "server": name}, nil
}

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
	sc := config.ServerConfig{
		Name:       raw.Name,
		Transport:  raw.Transport,
		URL:        raw.URL,
		Command:    raw.Command,
		Args:       raw.Args,
		AgentAdded: true,
	}
	if err := s.checkAgentTransport(sc); err != nil {
		return config.ServerConfig{}, err
	}
	return sc, nil
}

func (s *Server) checkAgentTransport(sc config.ServerConfig) error {
	if !sc.IsHTTPTransport() {
		if !s.cfg.DangerousAllowRuntimeStdio {
			return errors.New(
				"only http/sse/streamable transports are allowed; set dangerous_allow_runtime_stdio: true to enable stdio",
			)
		}
		return nil
	}
	if s.cfg.DangerousAllowPrivateURLs {
		return nil
	}
	return transport.ValidateURL(sc.URL)
}

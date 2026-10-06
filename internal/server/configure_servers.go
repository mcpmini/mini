package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
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
	if s.isUpstreamRegistered(sc.Name) {
		return errAlreadyRunning(sc.Name)
	}
	// Checked before changeSavedServer, which would stop a configured server's login or retries.
	if config.ServerFileExists(s.configDir, sc.Name) {
		return errAlreadyConfigured(sc.Name)
	}
	err := s.changeSavedServer(sc.Name, func() error {
		_, err := ops.AddServer(s.configDir, sc)
		return err
	})
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		return errAlreadyConfigured(sc.Name)
	}
	if err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	saved, err := s.connectSaved(ctx, sc.Name)
	if err != nil {
		return errors.Join(err, s.rollBackAdd(sc.Name))
	}
	s.recordConfigServers([]config.ServerConfig{saved})
	return nil
}

func (s *Server) connectSaved(ctx context.Context, name string) (config.ServerConfig, error) {
	saved, err := config.LoadServer(s.configDir, name)
	if err != nil {
		return config.ServerConfig{}, fmt.Errorf("add_server: %w", err)
	}
	err = s.addUpstream(ctx, s.newServerInstall(saved))
	if errors.Is(err, errAlreadyRegistered) {
		return config.ServerConfig{}, errAlreadyRunning(name)
	}
	return saved, err
}

func (s *Server) rollBackAdd(name string) error {
	if err := s.removeSavedServer(name); err != nil {
		return fmt.Errorf(
			"add_server: %s is still saved, so it will start next time; remove it with remove_server: %w",
			name,
			err,
		)
	}
	return nil
}

func (s *Server) removeSavedServer(name string) error {
	return s.changeSavedServer(name, func() error { return ops.RemoveServer(s.configDir, name) })
}

// changeSavedServer runs change, which adds or deletes the files saved for name, with nothing
// still running for the name able to write them. persistMu keeps a set_projection out.
func (s *Server) changeSavedServer(name string, change func() error) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	// A token refresh still running would save its token after change clears it.
	s.detachAndCloseServer(name)
	err := change()
	// Stops a login or install that started for the name during change.
	s.detachAndCloseServer(name)
	return err
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
	if err := s.removeSavedServer(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf(
			"remove_server: %s is disconnected but still saved, so it will start next time: %w",
			name,
			err,
		)
	}
	s.logger.Info("server removed by the config tool", "server", name)
	return map[string]any{"ok": true, "server": name}, nil
}

func (s *Server) detachAndCloseServer(serverName string) {
	s.cancelExistingAuthFlow(serverName)
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	s.detachAndCloseLocked(serverName)
}

func (s *Server) detachAndCloseLocked(serverName string) {
	s.removeGen[serverName]++
	if u := s.detachUpstream(serverName); u != nil {
		u.shutdownAndClose()
	}
	s.sessions.closeServerConnections(serverName)
	s.providerRegistry.Forget(serverName)
	s.reg.RemoveServer(serverName)
}

func (s *Server) detachUpstream(serverName string) *upstreamServer {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	u := s.upstreams[serverName]
	delete(s.upstreams, serverName)
	delete(s.projections, serverName)
	delete(s.configServers, serverName)
	return u
}

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
	_, err := ops.AddServer(s.configDir, sc)
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		return fmt.Errorf("add_server: %s is already configured; remove it with remove_server first", sc.Name)
	}
	if err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	// A login started for an earlier server of this name would install that server over this one.
	s.detachAndCloseServer(sc.Name)
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
		return fmt.Errorf("add_server: %s is still saved, so it will start next time; remove it with remove_server: %w", name, err)
	}
	return nil
}

func (s *Server) removeSavedServer(name string) error {
	// A token refresh still running would save the token again after its file is deleted.
	s.detachAndCloseServer(name)
	err := ops.RemoveServer(s.configDir, name)
	// Stops a login or install that started for the name before its files were gone.
	s.detachAndCloseServer(name)
	return err
}

func errAlreadyRunning(name string) error {
	return fmt.Errorf("add_server: %s is already running; remove it with remove_server first", name)
}

func (s *Server) removeServerFromAgent(name string) (any, error) {
	if err := validateServerName(name); err != nil {
		return nil, fmt.Errorf("remove_server: %w", err)
	}
	unlock := s.serverNames.lock(name)
	defer unlock()
	if err := s.removeSavedServer(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove_server: %s is disconnected but still saved, so it will start next time: %w", name, err)
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

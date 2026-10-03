//go:build test

package server

import (
	"context"

	"github.com/mcpmini/mini/internal/config"
)

func (s *Server) RunConfigReload(ctx context.Context, afterCheck func()) {
	s.runConfigReload(ctx, afterCheck)
}

func (s *Server) ReplaceProjections(p map[string]map[string]*config.ProjectionConfig) {
	s.replaceProjections(p, config.Servers{})
}

func (s *Server) WaitForStartupConnects() { s.connector.wait() }

const ConfigPollInterval = configPollInterval

// NameLockCallers counts the calls holding or waiting on name's lock.
func (s *Server) NameLockCallers(name string) int {
	s.serverNames.mu.Lock()
	defer s.serverNames.mu.Unlock()
	if nl := s.serverNames.locks[name]; nl != nil {
		return nl.refs
	}
	return 0
}

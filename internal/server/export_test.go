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
	s.replaceProjections(config.LoadProjectionsResult{Projections: p})
}

func (s *Server) WaitForStartupConnects() { s.connects.wait() }

const ConfigPollInterval = configPollInterval

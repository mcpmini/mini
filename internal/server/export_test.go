//go:build test

package server

import (
	"context"

	"github.com/mcpmini/mini/internal/config"
)

func (s *Server) RunConfigReload(ctx context.Context, baseline ConfigBaseline, afterCheck func()) {
	s.runConfigReload(ctx, baseline, afterCheck)
}

func (s *Server) ReplaceProjections(p map[string]map[string]*config.ProjectionConfig) {
	s.replaceProjections(config.LoadProjectionsResult{Projections: p})
}

func (s *Server) WaitForStartupConnects() { s.connectWg.Wait() }

const ConfigPollInterval = configPollInterval

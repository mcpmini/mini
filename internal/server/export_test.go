//go:build test

package server

import (
	"context"

	"github.com/mcpmini/mini/internal/config"
)

func (s *Server) RunProjectionReload(ctx context.Context, afterCheck func()) {
	s.runProjectionReload(ctx, afterCheck)
}

func (s *Server) ReplaceProjections(p map[string]map[string]*config.ProjectionConfig) {
	configured := make(map[string]struct{}, len(p))
	for name := range p {
		configured[name] = struct{}{}
	}
	s.replaceProjections(p, configured)
}

func (s *Server) WaitForStartupConnects() { s.connectWg.Wait() }

const ProjectionPollInterval = projectionPollInterval

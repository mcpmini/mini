//go:build test

package server

import "context"

func (s *Server) RunProjectionReload(ctx context.Context, afterCheck func()) {
	s.runProjectionReload(ctx, afterCheck)
}

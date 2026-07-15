//go:build test

package server

import "context"

// RunProjectionReload exposes the projection poll loop with a per-tick callback
// so external tests can synchronize on poll completion. Blocks until ctx is canceled.
func (s *Server) RunProjectionReload(ctx context.Context, afterCheck func()) {
	s.runProjectionReload(ctx, afterCheck)
}

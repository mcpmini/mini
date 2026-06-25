package server

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func newInternalConfigTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSetServerProjectionWaitsForPersistLockBeforeMemoryUpdate(t *testing.T) {
	srv := newInternalConfigTestServer(t)
	srv.persistMu.Lock()

	done := make(chan error, 1)
	go func() {
		_, err := srv.setServerProjection(configureParams{
			ServerName: "svc",
			Tool:       "myTool",
			Projection: &config.ProjectionConfig{Exclude: []string{"secret"}},
		}, "myTool")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("setServerProjection completed while persistMu was held: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if p := srv.projectionForTest("svc", "myTool"); p != nil {
		t.Fatal("projection was visible in memory before persistence lock was acquired")
	}

	srv.persistMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("setServerProjection failed after releasing persistMu: %v", err)
	}
	if p := srv.projectionForTest("svc", "myTool"); p == nil {
		t.Fatal("projection was not stored after persistence completed")
	}
}

func (s *Server) projectionForTest(serverName, tool string) *config.ProjectionConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.projections[serverName] == nil {
		return nil
	}
	return s.projections[serverName][tool]
}

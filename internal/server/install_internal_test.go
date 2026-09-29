//go:build test

package server

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func newInstallTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoveConfigServer_afterRemoveServerAndReAdd_keepsTheNewServer(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	srv.detachAndCloseServer("svc")
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{}); err != nil {
		t.Fatal(err)
	}

	if srv.removeConfigServer("svc") {
		t.Error("a pending config removal removed the server added after remove_server")
	}
	if !srv.isUpstreamRegistered("svc") {
		t.Error("re-added server svc is gone")
	}
}

func TestInstallChecked_guardRejection_closesConn(t *testing.T) {
	srv := newInstallTestServer(t)

	srv.serverOpMu.Lock()
	srv.removeGen["svc"]++
	srv.serverOpMu.Unlock()

	fake := &transport.FakeConnection{}
	in := upstreamInstall{cfg: config.ServerConfig{Name: "svc"}, removeGen: 0}

	err := srv.installChecked(fake, nil, in)

	if !errors.Is(err, errServerRemoved) {
		t.Errorf("expected errServerRemoved, got %v", err)
	}
	if !fake.Closed {
		t.Error("expected connection to be closed on guard rejection")
	}
}

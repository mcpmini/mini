//go:build test

package server

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

func TestRemoveConfigServer_keepsANameSavedAgainSinceTheServerSetWasLoaded(t *testing.T) {
	srv := newInstallTestServer(t)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{}); err != nil {
		t.Fatal(err)
	}
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	path := filepath.Join(srv.configDir, "servers", "svc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("command: run\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if srv.removeConfigServer("svc") {
		t.Error("removeConfigServer removed svc while its file is saved and enabled")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !srv.removeConfigServer("svc") {
		t.Error("removeConfigServer kept svc after its file was deleted")
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

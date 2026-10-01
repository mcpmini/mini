//go:build test

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
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

func TestAddServerFromAgent_aFailedAddStopsAnInstallStartedMeanwhile(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowPrivateURLs = true
	startedDuringTheAdd := srv.replacingInstall(config.ServerConfig{Name: "svc"})

	if _, err := srv.addServerFromAgent(t.Context(), &config.ServerConfig{Name: "svc", Transport: "http", URL: "http://127.0.0.1:1/mcp"}); err == nil {
		t.Fatal("add_server to an unreachable URL succeeded")
	}

	if err := srv.installChecked(&transport.FakeConnection{}, nil, startedDuringTheAdd); !errors.Is(err, errServerRemoved) {
		t.Errorf("install started during the failed add = %v, want errServerRemoved so no unsaved server runs", err)
	}
}

func TestAddServerFromAgent_stopsAnInstallStartedForTheNamesEarlierServer(t *testing.T) {
	echomcp := os.Getenv("ECHOMCP_BIN")
	if echomcp == "" {
		t.Fatal("ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...")
	}
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowRuntimeStdio = true
	startedForTheEarlierServer := srv.replacingInstall(config.ServerConfig{Name: "svc", Command: "earlier"})

	if _, err := srv.addServerFromAgent(t.Context(), &config.ServerConfig{Name: "svc", Command: echomcp}); err != nil {
		t.Fatal(err)
	}

	if err := srv.installChecked(&transport.FakeConnection{}, nil, startedForTheEarlierServer); !errors.Is(err, errServerRemoved) {
		t.Errorf("install for the earlier svc = %v, want errServerRemoved so it can't replace the added one", err)
	}
}

func TestRemoveServerFromAgent_holdsTheNameUntilTheServerIsDetached(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowPrivateURLs = true
	path := filepath.Join(srv.configDir, "servers", "svc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("transport: http\nurl: http://127.0.0.1:1/mcp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	srv.authMu.Lock() // the remove's detach starts by taking authMu, so it waits here after deleting the file
	removed := make(chan struct{})
	go func() { defer close(removed); _, _ = srv.removeServerFromAgent("svc") }() // the outcome checked is the add's
	waitUntil(func() bool { _, err := os.Stat(path); return errors.Is(err, fs.ErrNotExist) })

	added := make(chan struct{})
	go func() {
		defer close(added)
		// The add fails to connect either way; the check is whether it saved svc mid-remove.
		_, _ = srv.addServerFromAgent(context.Background(), &config.ServerConfig{Name: "svc", Transport: "http", URL: "http://127.0.0.1:1/mcp"})
	}()
	waitUntil(func() bool { _, err := os.Stat(path); return srv.NameLockCallers("svc") == 2 || err == nil })
	_, err := os.Stat(path)
	srv.authMu.Unlock()
	<-removed
	<-added

	if err == nil {
		t.Error("add_server saved svc while remove_server of svc was still detaching it")
	}
}

func waitUntil(condition func() bool) {
	for !condition() {
		runtime.Gosched()
	}
}

func TestRetryStartupAfter_stopsForACommandAnAgentMayNotRun(t *testing.T) {
	srv := newInstallTestServer(t)
	refused := fmt.Errorf("connect to svc: svc: %w", invoke.ErrAgentCommandNotAllowed)

	if srv.retryStartupAfter("svc", refused, time.Second) {
		t.Error("startup retries a command dangerous_allow_runtime_stdio doesn't allow, so it warns forever")
	}
	if !srv.retryStartupAfter("svc", errors.New("connection refused"), time.Second) {
		t.Error("startup gave up on an ordinary connect failure")
	}
}

func TestRollBackAdd_reportsAServerItCouldNotRemove(t *testing.T) {
	srv := newInstallTestServer(t)

	if err := srv.rollBackAdd("svc"); err == nil || !strings.Contains(err.Error(), "remove it with remove_server") {
		t.Errorf("rollBackAdd = %v, want the agent told the server is still saved", err)
	}
}

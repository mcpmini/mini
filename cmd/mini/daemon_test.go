package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
)

func executeDaemonCommand(configDir string) error {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--config", configDir, "daemon"})
	return cmd.Execute()
}

func TestDaemonCommandConfigFailureReturnsError(t *testing.T) {
	dir := testutil.ShortTempDir(t)
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), "[invalid")
	testutil.ErrorContains(t, executeDaemonCommand(dir), "load config:")
}

func TestDaemonCommandSocketSetupFailureReturnsError(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	socket := daemon.SocketPath(dir)
	if err := os.MkdirAll(filepath.Join(socket, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.ErrorContains(t, executeDaemonCommand(dir), socket)
}

func TestDaemonCommandAlreadyRunningReturnsError(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	testutil.StartUnixServer(t, daemon.SocketPath(dir), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	testutil.ErrorContains(t, executeDaemonCommand(dir), "daemon already running")
}

func TestDaemonCommandTokenFailureRemovesSocket(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	if err := os.MkdirAll(daemon.TokenFile(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.ErrorContains(t, executeDaemonCommand(dir), "write daemon token:")
	if _, err := os.Lstat(daemon.SocketPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket stat error = %v, want token failure to remove socket", err)
	}
}

func TestServeDaemonTokenFailureClosesListener(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	if err := os.MkdirAll(daemon.TokenFile(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := bindSocket(socketBindParams{Socket: daemon.SocketPath(dir), Chmod: os.Chmod})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := serveDaemon(context.Background(), DaemonServeParams{ConfigDir: dir, Listener: ln}); err == nil {
		t.Fatal("serveDaemon succeeded despite blocked token file")
	}
	if err := ln.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener Close error = %v, want already-closed listener", err)
	}
}

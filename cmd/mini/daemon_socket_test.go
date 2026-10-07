package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
)

func requireUnixSockets(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets are unavailable")
	}
}

func TestBindSocketDirectoryChmodFailurePreventsListen(t *testing.T) {
	requireUnixSockets(t)
	dir := filepath.Join(testutil.ShortTempDir(t), "internal", "daemon")
	socket := filepath.Join(dir, "mini.sock")
	wantErr := errors.New("chmod denied")
	calls := 0

	ln, err := bindSocket(socketBindParams{
		Socket: socket,
		Chmod: func(path string, mode os.FileMode) error {
			calls++
			if calls > 1 {
				return os.Chmod(path, mode)
			}
			if path != dir || mode != 0o700 {
				t.Fatalf("Chmod(%q, %04o), want (%q, 0700)", path, mode, dir)
			}
			return wantErr
		},
	})
	if ln != nil {
		t.Cleanup(func() { _ = ln.Close() })
	}
	if !errors.Is(err, wantErr) || ln != nil {
		t.Fatalf("bindSocket() = (%v, %v), want (nil, chmod error)", ln, err)
	}
	if calls != 1 {
		t.Fatalf("Chmod calls = %d, want 1", calls)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket stat error = %v, want not-exist", err)
	}
}

func TestBindSocketSocketChmodFailureClosesListener(t *testing.T) {
	requireUnixSockets(t)
	dir := filepath.Join(testutil.ShortTempDir(t), "internal", "daemon")
	socket := filepath.Join(dir, "mini.sock")
	wantErr := errors.New("chmod denied")

	ln, err := bindSocket(socketBindParams{
		Socket: socket,
		Chmod: func(path string, mode os.FileMode) error {
			if path == socket {
				return wantErr
			}
			return os.Chmod(path, mode)
		},
	})
	if !errors.Is(err, wantErr) || ln != nil {
		t.Fatalf("bindSocket() = (%v, %v), want (nil, chmod error)", ln, err)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket stat error = %v, want listener cleanup to remove socket", err)
	}
}

func TestBindSocketEnforcesModesAndCloses(t *testing.T) {
	requireUnixSockets(t)
	dir := filepath.Join(testutil.ShortTempDir(t), "internal", "daemon")
	socket := filepath.Join(dir, "mini.sock")

	ln, err := bindSocket(socketBindParams{Socket: socket, Chmod: os.Chmod})
	if err != nil {
		t.Fatalf("bindSocket() error = %v", err)
	}
	if ln == nil {
		t.Fatal("bindSocket() returned nil listener")
	}
	t.Cleanup(func() { _ = ln.Close() })
	for path, want := range map[string]os.FileMode{dir: 0o700, socket: 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode for %q = %04o, want %04o", path, got, want)
		}
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}
}

func TestBindSocketHealthyDaemonIsNoOp(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	socket := daemon.SocketPath(dir)
	testutil.StartUnixServer(t, socket, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ln, err := bindSocket(socketBindParams{Socket: socket, Chmod: os.Chmod})
	if err != nil || ln != nil {
		t.Fatalf("bindSocket() = (%v, %v), want (nil, nil)", ln, err)
	}
	resp, err := daemon.SocketClient(socket, time.Second).Get("http://localhost/healthz")
	if err != nil {
		t.Fatalf("healthy daemon request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatalf("healthy daemon socket was removed: %v", err)
	}
}

func TestBindSocketReclaimsStaleSocket(t *testing.T) {
	requireUnixSockets(t)
	dir := testutil.ShortTempDir(t)
	socket := daemon.SocketPath(dir)
	testutil.WriteFile(t, socket, "stale")

	ln, err := bindSocket(socketBindParams{Socket: socket, Chmod: os.Chmod})
	if err != nil {
		t.Fatalf("bindSocket() error = %v", err)
	}
	if ln == nil {
		t.Fatal("bindSocket() returned nil listener")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}
}

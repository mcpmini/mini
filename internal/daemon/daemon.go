// Package daemon provides helpers for detecting, starting, and communicating
// with a running mini daemon process.
package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func SocketPath(configDir string) string {
	return filepath.Join(configDir, "internal", "daemon", "daemon.sock")
}

// sun_path caps Unix socket paths at 104 bytes on macOS, 108 on Linux; 100 stays under both.
const maxSocketPathLen = 100

func CheckSocketPath(configDir string) error {
	if p := SocketPath(configDir); len(p) > maxSocketPathLen {
		return fmt.Errorf("daemon socket path is too long (%d > %d bytes): %s — use a shorter --config directory", len(p), maxSocketPathLen, p)
	}
	return nil
}

func Running(configDir string) bool {
	return SocketHealthy(SocketPath(configDir))
}

func SocketHealthy(socket string) bool {
	resp, err := SocketClient(socket, 2*time.Second).Get("http://localhost/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func SocketClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: socketTransport(socket)}
}

func socketTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// network and address come from the request URL; ignored because the daemon listens on a Unix socket
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
}

func Start(configDir string, timeout time.Duration, clock clock.Clock) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	release, err := acquireSpawnLock(configDir)
	if err != nil {
		return fmt.Errorf("acquire daemon spawn lock: %w", err)
	}
	defer release()
	if Running(configDir) {
		return nil
	}
	if err := spawnDaemon(exe, configDir); err != nil {
		return err
	}
	return waitForDaemon(configDir, timeout, clock)
}

func spawnDaemon(exe, configDir string) error {
	cmd := exec.Command(exe, "--config", configDir, "daemon")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

func waitForDaemon(configDir string, timeout time.Duration, clock clock.Clock) error {
	deadline := clock.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if Running(configDir) {
			return nil
		}
		sleep := clock.NewTimer(100 * time.Millisecond)
		select {
		case <-sleep.Chan():
		case <-deadline.Chan():
			sleep.Stop()
			return fmt.Errorf("daemon did not start within %v", timeout)
		}
	}
}

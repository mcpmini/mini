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
)

// SocketPath returns the daemon's Unix socket path in configDir. The socket lives in the
// per-user-private configDir rather than on a loopback TCP port so there is no shared port for
// another local user to squat or a browser to reach via DNS rebinding. See docs/daemon.md.
func SocketPath(configDir string) string {
	return filepath.Join(configDir, "daemon.sock")
}

// sun_path caps Unix socket paths at 104 bytes on macOS, 108 on Linux; 100 stays under both.
const maxSocketPathLen = 100

// CheckSocketPath errors if configDir's socket path would exceed the kernel limit, rather than
// leaving it to fail at bind/dial with a cryptic EINVAL.
func CheckSocketPath(configDir string) error {
	if p := SocketPath(configDir); len(p) > maxSocketPathLen {
		return fmt.Errorf("daemon socket path is too long (%d > %d bytes): %s — use a shorter --config directory", len(p), maxSocketPathLen, p)
	}
	return nil
}

// Running reports whether a live daemon answers /healthz on its socket; a stale socket file fails the probe.
func Running(configDir string) bool {
	return healthcheck(SocketPath(configDir))
}

func healthcheck(socket string) bool {
	resp, err := SocketClient(socket, 2*time.Second).Get("http://localhost/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SocketClient returns an HTTP client that dials the daemon's Unix socket; the request URL host
// is ignored by the dialer, so callers use http://localhost (which passes the loopback-Host check).
func SocketClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: SocketTransport(socket)}
}

func SocketTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
}

// Start launches a daemon in the background and waits up to timeout for it to be ready.
func Start(configDir string, timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	release, err := acquireSpawnLock(configDir)
	if err != nil {
		return fmt.Errorf("acquire daemon spawn lock: %w", err)
	}
	defer release()
	// Losers blocked on acquireSpawnLock arrive here to find the daemon already up.
	if Running(configDir) {
		return nil
	}
	if err := spawnDaemon(exe, configDir); err != nil {
		return err
	}
	// Lock held across waitForDaemon so racers waiting on acquireSpawnLock find the daemon already up.
	return waitForDaemon(configDir, timeout)
}

func spawnDaemon(exe, configDir string) error {
	cmd := exec.Command(exe, "--config", configDir, "daemon")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

func waitForDaemon(configDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if Running(configDir) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within %v", timeout)
}

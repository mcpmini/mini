// Package daemon provides helpers for detecting, starting, and communicating
// with a running mini daemon process.
package daemon

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PortFile returns the path to the daemon port file in configDir.
func PortFile(configDir string) string {
	return filepath.Join(configDir, "daemon.port")
}

// RunningPort returns the TCP port the daemon is listening on, or 0 if not running.
func RunningPort(configDir string) int {
	port, err := readPortFile(PortFile(configDir))
	if err != nil {
		return 0
	}
	if !healthcheckPort(port) {
		return 0
	}
	return port
}

func readPortFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func healthcheckPort(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Start launches a daemon in the background and waits up to timeout for it to be ready.
//
// At scale many proxies can call Start at the same instant when a shared daemon dies.
// The OS socket bind (startDaemonHTTP) already guarantees only one of the spawned daemons
// keeps the fixed port — the rest fail with EADDRINUSE and exit — so correctness never
// depends on this function. To avoid that wasted-spawn thundering herd, Start serializes on
// a cross-process flock: the lock winner spawns, every other caller blocks until the winner
// releases and then finds the daemon already up via the RunningPort re-check below. flock is
// released by the kernel on process death (even SIGKILL), so a spawner crashing mid-start
// can never deadlock the waiters.
func Start(configDir string, timeout time.Duration) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}
	release, err := acquireSpawnLock(configDir)
	if err != nil {
		return 0, fmt.Errorf("acquire daemon spawn lock: %w", err)
	}
	defer release()
	if port := RunningPort(configDir); port != 0 {
		return port, nil
	}
	if err := spawnDaemon(exe, configDir); err != nil {
		return 0, err
	}
	return waitForDaemon(configDir, timeout)
}

func spawnDaemon(exe, configDir string) error {
	cmd := exec.Command(exe, "--config", configDir, "daemon")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

func waitForDaemon(configDir string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if port := RunningPort(configDir); port != 0 {
			return port, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("daemon did not start within %v", timeout)
}

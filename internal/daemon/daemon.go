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
func Start(configDir string, timeout time.Duration) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}
	if err := spawnDaemon(exe, configDir); err != nil {
		return 0, err
	}
	return waitForDaemon(configDir, timeout)
}

func spawnDaemon(exe, configDir string) error {
	logFile, closeLog := openDaemonLog(configDir)
	defer closeLog() // safe: cmd.Start() dups the fd into the child process
	cmd := exec.Command(exe, "--config", configDir, "daemon")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

const maxDaemonLogBytes = 10 << 20 // 10MB

func openDaemonLog(configDir string) (*os.File, func()) {
	logPath := filepath.Join(configDir, "daemon.log")
	flag := logFileFlag(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|flag|os.O_WRONLY, 0600)
	if err != nil {
		return os.Stderr, func() {}
	}
	return f, func() { f.Close() }
}

func logFileFlag(logPath string) int {
	info, err := os.Stat(logPath)
	if err == nil && info.Size() >= maxDaemonLogBytes {
		return os.O_TRUNC
	}
	return os.O_APPEND
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

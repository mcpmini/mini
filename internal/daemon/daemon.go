// Package daemon provides helpers for detecting, starting, and communicating
// with a running mini daemon process.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
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

// TokenFile returns the path to the daemon bearer-token file in configDir.
func TokenFile(configDir string) string {
	return filepath.Join(configDir, "daemon.token")
}

// GenerateToken returns a cryptographically random 32-byte token as a hex string.
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// WriteToken mints a new token and writes it to TokenFile(configDir), and returns it.
// It removes any stale file first and creates the new one with O_EXCL so the 0600 mode
// is guaranteed even if a crashed daemon left a file with looser permissions behind.
func WriteToken(configDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	path := TokenFile(configDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.WriteString(token); err != nil {
		return "", err
	}
	return token, nil
}

// ReadToken reads the daemon bearer token from TokenFile(configDir).
func ReadToken(configDir string) (string, error) {
	data, err := os.ReadFile(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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

package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

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

// WriteToken mints a fresh token and atomically writes it to TokenFile(configDir).
func WriteToken(configDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	return token, atomicWriteFile(TokenFile(configDir), token)
}

// ReadToken reads the daemon bearer token from TokenFile(configDir).
func ReadToken(configDir string) (string, error) {
	data, err := os.ReadFile(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// atomicWriteFile writes data to path via a temp file and a rename, so a concurrent
// reader never observes a partial or empty file and the result is 0600 even when it
// replaces a stale file with looser permissions.
func atomicWriteFile(path, data string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	// Cleanup only; the write/rename error that set err is the one worth returning.
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.WriteString(data); err != nil {
		_ = tmp.Close() // already failing; the temp is discarded, nothing to bubble up
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

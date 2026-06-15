// Localhost alone is not an auth boundary — any local process or browser-driven
// request can reach the daemon (DNS rebinding, SSRF, malicious browser extensions).
// To reduce this attack surface, clients must present a bearer token stored on disk.
// The daemon mints a fresh token on every start; clients read it from the token file
// and automatically pick up a rotated token when the daemon restarts.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

func TokenFile(configDir string) string {
	return filepath.Join(configDir, "daemon.token")
}

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func WriteToken(configDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	return token, atomicWriteFile(TokenFile(configDir), token)
}

func ReadToken(configDir string) (string, error) {
	data, err := os.ReadFile(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Standard atomic-write pattern (CreateTemp + Rename); see github.com/natefinch/atomic
// for the canonical Go implementation. We inline it to force 0600 (natefinch preserves
// existing perms, which would weaken security if a stale file had looser mode).
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

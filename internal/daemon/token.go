// Localhost alone is not an auth boundary — any local process or browser-driven
// request can reach the daemon (DNS rebinding, SSRF, malicious browser extensions).
// To reduce this attack surface, clients must present a bearer token stored on disk.
// The daemon reuses a persisted token across restarts (see EnsureToken) so a respawned
// daemon keeps the same token and already-connected proxies aren't rejected with 401.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

// EnsureToken reuses the stored daemon token if it is present, non-empty, AND owner-only
// (0600); otherwise it mints and persists a new one. A looser-permission file may already
// have been read by another local user, so it is treated as compromised and re-minted rather
// than trusted. Reusing a secure token lets already-connected proxies keep working across a
// daemon respawn instead of being rejected with 401.
func EnsureToken(configDir string) (string, error) {
	if tok, err := readPrivateToken(configDir); err == nil && tok != "" {
		return tok, nil
	}
	return WriteToken(configDir)
}

// readPrivateToken returns the stored token only when the file is owner-only; any group or
// other permission bit makes it return an error so EnsureToken re-mints a fresh token.
func readPrivateToken(configDir string) (string, error) {
	info, err := os.Stat(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file has insecure permissions %#o", info.Mode().Perm())
	}
	return ReadToken(configDir)
}

func atomicWriteFile(path, data string) (err error) {
	// Write to a temp file then rename so concurrent readers never see partial content.
	// https://github.com/natefinch/atomic/blob/59b8c279e6d5/atomic.go#L17
	// CreateTemp guarantees 0600 regardless of prior file perms.
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

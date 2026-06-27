// The daemon's access boundary is the Unix socket under the per-user-private configDir (see
// SocketPath); this bearer token is defense-in-depth. It persists across restarts (see
// EnsureToken) so a respawned daemon keeps the same token and connected proxies aren't 401'd.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/randutil"
)

func TokenFile(configDir string) string {
	return filepath.Join(configDir, "internal", "daemon", "daemon.token")
}

func GenerateToken() string {
	return randutil.HexString(32)
}

func WriteToken(configDir string) (string, error) {
	token := GenerateToken()
	path := TokenFile(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	return token, atomicWriteFile(path, token)
}

func ReadToken(configDir string) (string, error) {
	data, err := os.ReadFile(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// EnsureToken returns the existing daemon token, re-minting it if missing or insecurely permissioned.
func EnsureToken(configDir string) (string, error) {
	if tok, err := readPrivateToken(configDir); err == nil && tok != "" {
		return tok, nil
	}
	return WriteToken(configDir)
}

func readPrivateToken(configDir string) (string, error) {
	info, err := os.Stat(TokenFile(configDir))
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		// Group/other-readable: another local user may have already read this; treat as compromised and re-mint.
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
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

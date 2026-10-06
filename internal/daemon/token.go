// The daemon's access boundary is the Unix socket under the per-user-private configDir (see
// SocketPath); this bearer token is defense-in-depth. It persists across restarts (see
// EnsureToken) so a respawned daemon keeps the same token and connected proxies aren't 401'd.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/fileio"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return token, fileio.ReplaceFile(path, []byte(token), fileio.ReplaceOptions{Perm: 0o600})
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

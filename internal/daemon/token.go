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

func atomicWriteFile(path, data string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
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
	// Atomic rewrite: the OS replace ensures concurrent readers never see partial content.
	// https://github.com/natefinch/atomic/blob/59b8c279e6d5/file_unix.go#L13
	return os.Rename(tmp.Name(), path)
}

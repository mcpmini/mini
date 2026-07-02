package auth

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/config"
)

func MarkOAuthDetected(configDir, serverName string) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	path := config.OAuthDetectedMarkerPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("{}"), 0600)
}

func IsOAuthDetected(configDir, serverName string) bool {
	_, err := os.Stat(config.OAuthDetectedMarkerPath(configDir, serverName))
	return err == nil
}

package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/config"
)

func MarkOAuthDetected(configDir, serverName string) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	path := config.ServerMetaPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(config.ServerMeta{OAuthDetected: true})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func IsOAuthDetected(configDir, serverName string) bool {
	data, err := os.ReadFile(config.ServerMetaPath(configDir, serverName))
	if err != nil {
		return false
	}
	var m config.ServerMeta
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	return m.OAuthDetected
}

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ServerMetaPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".meta.json")
}

// ServerMeta holds machine-detected facts about a server that don't belong in its
// user-editable YAML.
type ServerMeta struct {
	OAuthDetected bool `json:"oauth_detected,omitempty"`
}

// MarkOAuthDetected records that serverName's upstream answered with a confirmed OAuth
// 401 challenge, so a future Load merges auth: type: oauth2 into it without the user
// hand-editing YAML.
func MarkOAuthDetected(configDir, serverName string) error {
	if !ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	path := ServerMetaPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(ServerMeta{OAuthDetected: true})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// IsOAuthDetected reports whether MarkOAuthDetected has previously recorded serverName.
func IsOAuthDetected(configDir, serverName string) bool {
	return readServerMeta(configDir, serverName).OAuthDetected
}

func readServerMeta(configDir, serverName string) ServerMeta {
	if !ValidServerName.MatchString(serverName) {
		return ServerMeta{}
	}
	data, err := os.ReadFile(ServerMetaPath(configDir, serverName))
	if err != nil {
		return ServerMeta{}
	}
	var m ServerMeta
	json.Unmarshal(data, &m) //nolint:errcheck
	return m
}

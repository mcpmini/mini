package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/config"
)

type Registration struct {
	ClientID string `json:"client_id"`
}

func LoadRegistration(configDir, serverName string) (*Registration, error) {
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	data, err := os.ReadFile(registrationPath(configDir, serverName))
	if err != nil {
		return nil, err
	}
	var r Registration
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func SaveRegistration(configDir, serverName string, r *Registration) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	dir := filepath.Join(configDir, "internal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registrationPath(configDir, serverName), data, 0600)
}

func registrationPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".client.json")
}

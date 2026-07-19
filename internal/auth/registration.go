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

	// Populated when the AS registered mini as confidential (RFC 7591 §3.2.1).
	// Zero in older public-client registration files without these fields.
	ClientSecret            string `json:"client_secret,omitempty"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method,omitempty"`
	ClientSecretExpiresAt   int64  `json:"client_secret_expires_at,omitempty"`
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
	path := registrationPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicReplaceFile(path, data)
}

func registrationPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".dcr.json")
}

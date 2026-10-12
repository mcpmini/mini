package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/defaults"
)

// ValidServerName matches the allowed character set for server names used in
// file paths. This is enforced at all input boundaries to prevent path traversal.
var ValidServerName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidToolName matches tool names: alphanumeric, underscores, hyphens, dots.
// Dots allow namespaced tools (e.g. "issues.list"). Slashes are excluded to
// prevent future path traversal risk if tool names are ever used in file paths.
var ValidToolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// Only ${VAR} form (not bare $VAR) avoids false positives in shell args and YAML comments.
var envVarRef = regexp.MustCompile(`\$\{([^}]+)\}`)

// LoadMain loads global settings and returns config.yaml parse and interpolation errors.
func LoadMain(configDir string) (*Config, error) {
	return loadMainConfig(configDir)
}

func loadMainConfig(dir string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := readMainConfigFile(dir)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return cfg, nil
	}
	if parseErr := yaml.Unmarshal(data, cfg); parseErr != nil {
		return nil, fmt.Errorf("parse config: %w", parseErr)
	}
	responseDir, err := expandEnvValue("response_dir", cfg.ResponseDir)
	if err != nil {
		return nil, fmt.Errorf("config.yaml: %w", err)
	}
	cfg.ResponseDir = responseDir
	if err := ValidResponseFormat(cfg.ResponseFormat); err != nil {
		return nil, fmt.Errorf("config.yaml: response_format: %w", err)
	}
	return cfg, nil
}

func readMainConfigFile(dir string) ([]byte, error) {
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return data, nil
}

// HasBundledAuth reports whether loading sc merges in a vendor's bundled auth config,
// which an explicit auth block in its file would shadow.
func (sc ServerConfig) HasBundledAuth() bool {
	return bundledAuth(sc) != nil
}

func bundledAuth(sc ServerConfig) *AuthConfig {
	key := defaults.MatchKnownServer(sc.Command, sc.Args, sc.URL)
	if key == "" {
		return nil
	}
	data := defaults.AuthFor(key)
	if data == nil {
		return nil
	}
	var ac AuthConfig
	if yaml.Unmarshal(data, &ac) != nil {
		return nil
	}
	return &ac
}

func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".mini"), nil
}

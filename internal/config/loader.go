package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

func readAndInterpolate(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	data, err = interpolateEnv(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}

func LoadActions(dir string) ([]ActionConfig, error) {
	pattern := filepath.Join(dir, "internal", "actions", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var actions []ActionConfig
	for _, p := range paths {
		a, err := loadActionConfig(p)
		if err != nil {
			return nil, err
		}
		actions = append(actions, *a)
	}
	return actions, nil
}

func loadActionConfig(p string) (*ActionConfig, error) {
	data, err := readAndInterpolate(p)
	if err != nil {
		return nil, err
	}
	return parseActionConfig(p, data)
}

func parseActionConfig(p string, data []byte) (*ActionConfig, error) {
	var a ActionConfig
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if a.Name == "" {
		a.Name = filepath.Base(p[:len(p)-5])
	}
	if err := validateActionConfig(p, a); err != nil {
		return nil, err
	}
	return &a, nil
}

func validateActionConfig(path string, a ActionConfig) error {
	if !ValidServerName.MatchString(a.Name) {
		return fmt.Errorf("action %s: invalid name %q", path, a.Name)
	}
	if a.Server != "" && !ValidServerName.MatchString(a.Server) {
		return fmt.Errorf("action %s: invalid server name %q", path, a.Server)
	}
	if a.Tool != "" && !ValidToolName.MatchString(a.Tool) {
		return fmt.Errorf("action %s: invalid tool name %q", path, a.Tool)
	}
	return nil
}

func interpolateEnv(data []byte) ([]byte, error) {
	var missing []string
	seen := map[string]bool{}
	result := envVarRef.ReplaceAllStringFunc(string(data), func(match string) string {
		key := match[2 : len(match)-1]
		val, ok := os.LookupEnv(key)
		if !ok && !seen[key] {
			seen[key] = true
			missing = append(missing, key)
		}
		return val
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("config references undefined environment variable(s): %s", strings.Join(missing, ", "))
	}
	return []byte(result), nil
}

func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".mini"), nil
}

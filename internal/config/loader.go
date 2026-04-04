package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// ValidServerName matches the allowed character set for server names used in
// file paths. This is enforced at all input boundaries to prevent path traversal.
var ValidServerName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidToolName matches tool names: alphanumeric, underscores, hyphens, dots.
// Dots allow namespaced tools (e.g. "issues.list"). Slashes are excluded to
// prevent future path traversal risk if tool names are ever used in file paths.
var ValidToolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func Load(configDir string) (*Config, []ServerConfig, error) {
	cfg, err := loadMainConfig(configDir)
	if err != nil {
		return nil, nil, err
	}

	servers, err := loadServerConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	servers = deduplicateServers(append(servers, cfg.Servers...))

	projections, err := loadProjectionConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	mergeProjections(servers, projections)

	return cfg, servers, nil
}

func loadProjectionConfigs(dir string) (map[string]map[string]*ProjectionConfig, error) {
	pattern := filepath.Join(dir, "projections", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]*ProjectionConfig)
	for _, p := range paths {
		if err := loadOneProjectionFile(out, p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func loadOneProjectionFile(out map[string]map[string]*ProjectionConfig, p string) error {
	serverName := filepath.Base(p[:len(p)-5])
	if !ValidServerName.MatchString(serverName) {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	var toolProjections map[string]*ProjectionConfig
	if err := yaml.Unmarshal(data, &toolProjections); err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}
	out[serverName] = toolProjections
	return nil
}

// mergeProjections overlays projection dir configs onto server configs.
// Projection dir wins over inline server config projections.
func mergeProjections(servers []ServerConfig, projections map[string]map[string]*ProjectionConfig) {
	for i := range servers {
		toolProjections, ok := projections[servers[i].Name]
		if !ok {
			continue
		}
		if servers[i].Projections == nil {
			servers[i].Projections = make(map[string]*ProjectionConfig)
		}
		for tool, p := range toolProjections {
			servers[i].Projections[tool] = p
		}
	}
}

func loadMainConfig(dir string) (*Config, error) {
	cfg := DefaultConfig()
	path := filepath.Join(dir, "config.yaml")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func loadServerConfigs(dir string) ([]ServerConfig, error) {
	pattern := filepath.Join(dir, "servers", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var servers []ServerConfig
	for _, p := range paths {
		s, err := loadServerConfig(p)
		if err != nil {
			return nil, err
		}
		servers = append(servers, *s)
	}
	return servers, nil
}

func loadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s ServerConfig
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if !ValidServerName.MatchString(s.Name) {
		return nil, fmt.Errorf("invalid server name %q in %s: must match ^[a-zA-Z0-9_-]+$", s.Name, path)
	}
	return &s, nil
}

// LoadActions reads ~/.mini/actions/*.yaml files.
// Each file defines a single ActionConfig.
func LoadActions(dir string) ([]ActionConfig, error) {
	pattern := filepath.Join(dir, "actions", "*.yaml")
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
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var a ActionConfig
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if a.Name == "" {
		a.Name = filepath.Base(p[:len(p)-5])
	}
	if !ValidServerName.MatchString(a.Name) {
		return nil, fmt.Errorf("action %s: invalid name %q", p, a.Name)
	}
	if a.Server != "" && !ValidServerName.MatchString(a.Server) {
		return nil, fmt.Errorf("action %s: invalid server name %q", p, a.Server)
	}
	if a.Tool != "" && !ValidToolName.MatchString(a.Tool) {
		return nil, fmt.Errorf("action %s: invalid tool name %q", p, a.Tool)
	}
	return &a, nil
}

// deduplicateServers removes later occurrences of servers with the same name.
// Directory-loaded servers (earlier in slice) take precedence over config.yaml inline servers.
func deduplicateServers(servers []ServerConfig) []ServerConfig {
	seen := make(map[string]bool, len(servers))
	var out []ServerConfig
	for _, s := range servers {
		if !seen[s.Name] {
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	return out
}

func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mini")
}

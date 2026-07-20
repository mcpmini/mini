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

func Load(configDir string) (*Config, []ServerConfig, error) {
	cfg, servers, err := loadBaseConfig(configDir)
	if err != nil {
		return nil, nil, err
	}
	projections, err := loadProjectionConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	mergeProjections(servers, projections)
	if err := validateResponseFormats(cfg, servers); err != nil {
		return nil, nil, err
	}
	return cfg, servers, nil
}

func validateResponseFormats(cfg *Config, servers []ServerConfig) error {
	if err := ValidResponseFormat(cfg.ResponseFormat); err != nil {
		return fmt.Errorf("config.yaml: response_format: %w", err)
	}
	for _, s := range servers {
		for tool, p := range s.Projections {
			if p == nil {
				continue
			}
			if err := ValidResponseFormat(p.Format); err != nil {
				return fmt.Errorf("server %s: projection %s: format: %w", s.Name, tool, err)
			}
		}
	}
	return nil
}

func loadBaseConfig(configDir string) (*Config, []ServerConfig, error) {
	cfg, err := loadMainConfig(configDir)
	if err != nil {
		return nil, nil, err
	}
	servers, err := loadServerConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	combined := deduplicateServers(append(servers, cfg.Servers...))
	mergeKnownAuth(configDir, combined)
	return cfg, combined, nil
}

func loadProjectionConfigs(dir string) (map[string]map[string]*ProjectionConfig, error) {
	pattern := filepath.Join(dir, "servers", "*.proj.yaml")
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
	serverName := strings.TrimSuffix(filepath.Base(p), ".proj.yaml")
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
	data, err := readMainConfigFile(dir)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
	return interpolateEnv(data)
}

func loadServerConfigs(dir string) ([]ServerConfig, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "servers", "*.yaml"))
	if err != nil {
		return nil, err
	}
	return loadServerFiles(filterServerPaths(paths))
}

// mergeKnownAuth fills in Auth from a bundled default or a prior detection marker —
// but never overrides a server's own auth: block.
func mergeKnownAuth(dir string, servers []ServerConfig) {
	for i := range servers {
		if servers[i].Auth != nil {
			continue
		}
		if ac := bundledAuth(servers[i]); ac != nil {
			servers[i].Auth = ac
			continue
		}
		if readServerMeta(dir, servers[i].Name).OAuthDetected {
			servers[i].Auth = &AuthConfig{Type: AuthTypeOAuth2}
		}
	}
}

func bundledAuth(sc ServerConfig) *AuthConfig {
	cmdLine := strings.ToLower(sc.Command + " " + strings.Join(sc.Args, " "))
	key := defaults.DetectKey(cmdLine, sc.URL)
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

func filterServerPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasSuffix(p, ".proj.yaml") {
			out = append(out, p)
		}
	}
	return out
}

func loadServerFiles(paths []string) ([]ServerConfig, error) {
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
	data, err := readAndInterpolate(path)
	if err != nil {
		return nil, err
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

func FindServer(servers []ServerConfig, name string) *ServerConfig {
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i]
		}
	}
	return nil
}

func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mini")
}

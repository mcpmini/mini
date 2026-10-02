package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// Loaded is the config as Load found it. A server whose file, or projection file, fails to load
// is left out of Servers and listed in Broken, so one bad file can't stop every other server.
type Loaded struct {
	Config  *Config
	Servers []ServerConfig
	Broken  []SourceError
}

// Load fails only when config.yaml does, since no server can run without it.
func Load(configDir string) (Loaded, error) {
	cfg, err := loadMainConfig(configDir)
	if err != nil {
		return Loaded{}, err
	}
	servers, broken := loadServerDir(configDir)
	return Loaded{Config: cfg, Servers: servers, Broken: broken}, nil
}

func loadServerDir(configDir string) ([]ServerConfig, []SourceError) {
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml")) // the pattern is constant, so it can't be malformed
	var servers []ServerConfig
	var broken []SourceError
	for _, path := range filterServerPaths(paths) {
		sc, err := loadCompleteServer(configDir, path)
		if err != nil {
			broken = append(broken, SourceError{Path: path, ServerName: serverNameFromPath(path), Err: err})
			continue
		}
		servers = append(servers, sc)
	}
	return servers, broken
}

// LoadServer loads one server as Load would, without needing every other server file to load.
func LoadServer(configDir, name string) (ServerConfig, error) {
	if err := checkServerName(name, "the request"); err != nil {
		return ServerConfig{}, err
	}
	path := ServerPath(configDir, name)
	if !ServerFileExists(configDir, name) {
		return ServerConfig{}, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return loadCompleteServer(configDir, path)
}

func loadCompleteServer(configDir, path string) (ServerConfig, error) {
	sc, err := loadServerConfig(path)
	if err != nil {
		return ServerConfig{}, err
	}
	projections := make(map[string]map[string]*ProjectionConfig)
	if err := loadOneProjectionFile(projections, ProjectionPath(configDir, sc.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ServerConfig{}, err
	}
	servers := []ServerConfig{*sc}
	mergeProjections(servers, projections)
	if err := validateServerProjectionFormats(sc.Name, servers[0].Projections); err != nil {
		return ServerConfig{}, err
	}
	mergeKnownAuth(configDir, servers)
	return servers[0], nil
}

func checkServerName(name, source string) error {
	if !ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q in %s: must match ^[a-zA-Z0-9_-]+$", name, source)
	}
	return nil
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
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	responseDir, err := expandEnvValue(cfg.ResponseDir, strictEnvExpansion)
	if err != nil {
		return nil, fmt.Errorf("config.yaml: response_dir: %w", err)
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
		// A marker can outlive the server that earned it, and an agent's server never gets OAuth.
		if !servers[i].AgentAdded && readServerMeta(dir, servers[i].Name).OAuthDetected {
			servers[i].Auth = &AuthConfig{Type: AuthTypeOAuth2}
		}
	}
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

func filterServerPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasSuffix(p, ".proj.yaml") {
			out = append(out, p)
		}
	}
	return out
}

func loadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseServerConfig(path, data, strictEnvExpansion)
}

func parseServerConfig(path string, data []byte, mode envExpansionMode) (*ServerConfig, error) {
	name := serverNameFromPath(path)
	if err := checkServerName(name, path); err != nil {
		return nil, err
	}
	var s ServerConfig
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Name = name
	if _, err := ParseTimeoutSpec(s.HandshakeTimeout, 0); err != nil {
		return nil, fmt.Errorf("invalid handshake_timeout in %s: %w", path, err)
	}
	if err := validateServerFields(path, &s, mode); err != nil {
		return nil, err
	}
	return &s, nil
}

func validateServerFields(source string, sc *ServerConfig, mode envExpansionMode) error {
	if err := checkUnexpandedFields(*sc); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if err := expandServerSecrets(sc, mode); err != nil {
		return fmt.Errorf("%s: server %s: %w", source, sc.Name, err)
	}
	return nil
}

func ServerPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".yaml")
}

func ProjectionPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".proj.yaml")
}

// ServerFileExists matches the name exactly: a case-insensitive disk would otherwise
// treat "GitHub" as github.yaml, and act on that server under the wrong name.
func ServerFileExists(configDir, name string) bool {
	path := ServerPath(configDir, name)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == filepath.Base(path) })
}

func serverNameFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".yaml")
}

// ValidateServerFile checks data as a server file at path, which names the server. An unset ${VAR}
// in a secret field passes, since it only has to be set where mini runs.
func ValidateServerFile(path string, data []byte) error {
	sc, err := parseServerConfig(path, data, lenientEnvExpansion)
	if err != nil {
		return err
	}
	return validateServerProjectionFormats(sc.Name, sc.Projections)
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

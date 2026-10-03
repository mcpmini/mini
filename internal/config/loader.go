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

func Load(configDir string) (*Config, []ServerConfig, error) {
	cfg, err := loadMainConfig(configDir)
	if err != nil {
		return nil, nil, err
	}
	servers, err := loadServerConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	projections, err := loadProjectionConfigs(configDir)
	if err != nil {
		return nil, nil, err
	}
	if err := completeServers(configDir, servers, projections); err != nil {
		return nil, nil, err
	}
	return cfg, servers, nil
}

// LoadServer loads one server as Load would, without needing every other server file to load.
func LoadServer(configDir, name string) (ServerConfig, error) {
	sc, err := loadNamedServerFile(configDir, name)
	if err != nil {
		return ServerConfig{}, err
	}
	projections := make(map[string]map[string]*ProjectionConfig)
	if err := loadOneProjectionFile(projections, ProjectionPath(configDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ServerConfig{}, err
	}
	servers := []ServerConfig{*sc}
	if err := completeServers(configDir, servers, projections); err != nil {
		return ServerConfig{}, err
	}
	return servers[0], nil
}

func loadNamedServerFile(configDir, name string) (*ServerConfig, error) {
	if err := checkServerName(name, "the request"); err != nil {
		return nil, err
	}
	path := ServerPath(configDir, name)
	if !ServerFileExists(configDir, name) {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return loadServerConfig(path)
}

func completeServers(configDir string, servers []ServerConfig, projections map[string]map[string]*ProjectionConfig) error {
	mergeProjections(servers, projections)
	for _, s := range servers {
		if s.ProjectionsErr != nil {
			return s.ProjectionsErr.Err
		}
		if err := validateServerProjectionFormats(s.Name, s.Projections); err != nil {
			return err
		}
	}
	for i := range servers {
		mergeKnownAuth(configDir, &servers[i])
	}
	return nil
}

func checkServerName(name, source string) error {
	if !ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q in %s: must match ^[a-zA-Z0-9_-]+$", name, source)
	}
	return nil
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

func loadServerConfigs(dir string) ([]ServerConfig, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "servers", "*.yaml"))
	if err != nil {
		return nil, err
	}
	return loadServerFiles(filterServerPaths(paths))
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseServerConfig(path, data)
}

func parseServerConfig(path string, data []byte) (*ServerConfig, error) {
	name := serverNameFromPath(path)
	if err := checkServerName(name, path); err != nil {
		return nil, err
	}
	var s ServerConfig
	inlineProjections, err := decodeServerFile(data, &s)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Name = name
	decodeInlineProjections(&s, path, inlineProjections)
	if _, err := ParseTimeoutSpec(s.HandshakeTimeout, 0); err != nil {
		return nil, fmt.Errorf("invalid handshake_timeout in %s: %w", path, err)
	}
	if err := checkUnexpandedFields(s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	expandServerEnv(&s)
	return &s, nil
}

// Inline projections decode apart from the rest of the file, so a mistake in them costs the
// server only its projections, as one in its projection file does.
func decodeServerFile(data []byte, s *ServerConfig) (inlineProjections *yaml.Node, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	inlineProjections = detachMappingValue(&doc, "projections")
	return inlineProjections, doc.Decode(s)
}

func detachMappingValue(doc *yaml.Node, key string) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			value := mapping.Content[i+1]
			mapping.Content = slices.Delete(mapping.Content, i, i+2)
			return value
		}
	}
	return nil
}

func decodeInlineProjections(s *ServerConfig, path string, node *yaml.Node) {
	if node == nil {
		return
	}
	if err := node.Decode(&s.Projections); err != nil {
		s.Projections = nil
		s.ProjectionsErr = &SourceError{Path: path, ServerName: s.Name, Err: fmt.Errorf("parse %s: %w", path, err)}
	}
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
// passes, since it only has to be set where mini runs.
func ValidateServerFile(path string, data []byte) error {
	sc, err := parseServerConfig(path, data)
	if err != nil {
		return err
	}
	if sc.ProjectionsErr != nil {
		return sc.ProjectionsErr.Err
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

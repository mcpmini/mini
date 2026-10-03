package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// Servers is the server files as this process loaded them. A server whose file, or projection
// file, fails to load is in Broken instead of Loaded, so it can't stop the others.
type Servers struct {
	Loaded []ServerConfig
	Broken []SourceError
}

// SourceError is a server whose file, or projection file, failed to load.
type SourceError struct {
	Path       string
	ServerName string
	Err        error
}

func LoadServers(configDir string) Servers {
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml")) // the pattern is constant, so it can't be malformed
	var servers Servers
	for _, path := range filterServerPaths(paths) {
		sc, err := loadServerFile(configDir, path)
		if err != nil {
			servers.Broken = append(servers.Broken, SourceError{Path: path, ServerName: serverNameFromPath(path), Err: err})
			continue
		}
		servers.Loaded = append(servers.Loaded, sc)
	}
	return servers
}

func (s Servers) Find(name string) (ServerConfig, bool) {
	i := slices.IndexFunc(s.Loaded, func(sc ServerConfig) bool { return sc.Name == name })
	if i < 0 {
		return ServerConfig{}, false
	}
	return s.Loaded[i], true
}

func (s Servers) IsBroken(name string) bool {
	return slices.ContainsFunc(s.Broken, func(b SourceError) bool { return b.ServerName == name })
}

func (s Servers) IsEnabled(name string) bool {
	sc, ok := s.Find(name)
	return ok && sc.IsEnabled()
}

// LoadServer loads one server as LoadServers would, without needing any other server file to load.
func LoadServer(configDir, name string) (ServerConfig, error) {
	if err := checkServerName(name, "the request"); err != nil {
		return ServerConfig{}, err
	}
	path := ServerPath(configDir, name)
	if !ServerFileExists(configDir, name) {
		return ServerConfig{}, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return loadServerFile(configDir, path)
}

func loadServerFile(configDir, path string) (ServerConfig, error) {
	sc, err := loadServerConfig(path)
	if err != nil {
		return ServerConfig{}, err
	}
	if err := mergeProjectionFile(sc, ProjectionPath(configDir, sc.Name)); err != nil {
		return ServerConfig{}, err
	}
	if err := validateServerProjectionFormats(sc.Name, sc.Projections); err != nil {
		return ServerConfig{}, err
	}
	mergeKnownAuth(configDir, sc)
	return *sc, nil
}

// mergeProjectionFile overlays the server's projection file onto its inline projections; the file wins.
func mergeProjectionFile(sc *ServerConfig, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var toolProjections map[string]*ProjectionConfig
	if err := yaml.Unmarshal(data, &toolProjections); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if sc.Projections == nil {
		sc.Projections = make(map[string]*ProjectionConfig, len(toolProjections))
	}
	for tool, p := range toolProjections {
		sc.Projections[tool] = p
	}
	return nil
}

// mergeKnownAuth fills in Auth from a bundled default or a prior detection marker,
// but never overrides a server's own auth: block.
func mergeKnownAuth(dir string, sc *ServerConfig) {
	if sc.Auth != nil {
		return
	}
	if ac := bundledAuth(*sc); ac != nil {
		sc.Auth = ac
		return
	}
	// A marker can outlive the server that earned it, and an agent's server never gets OAuth.
	if !sc.AgentAdded && readServerMeta(dir, sc.Name).OAuthDetected {
		sc.Auth = &AuthConfig{Type: AuthTypeOAuth2}
	}
}

func validateServerProjectionFormats(name string, projections map[string]*ProjectionConfig) error {
	for tool, p := range projections {
		if p == nil {
			continue
		}
		if err := ValidResponseFormat(p.Format); err != nil {
			return fmt.Errorf("server %s: projection %s: format: %w", name, tool, err)
		}
	}
	return nil
}

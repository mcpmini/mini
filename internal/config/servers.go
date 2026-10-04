package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Servers struct {
	Loaded []ServerConfig
	Broken []SourceError
}

type SourceError struct {
	Path       string
	ServerName string
	Err        error
}

// LoadServers fails only when it can't list the server files: an empty result must mean no servers,
// never "couldn't look", or a reload would remove every running one.
func LoadServers(configDir string) (Servers, error) {
	paths, err := ServerDirFiles(configDir)
	if err != nil {
		return Servers{}, err
	}
	var servers Servers
	for _, path := range filterServerPaths(paths) {
		sc, err := loadServerFile(configDir, path)
		if err != nil {
			servers.Broken = append(servers.Broken, SourceError{Path: path, ServerName: serverNameFromPath(path), Err: err})
			continue
		}
		servers.Loaded = append(servers.Loaded, sc)
	}
	return servers, nil
}

func ServerDirFiles(configDir string) ([]string, error) {
	dir := filepath.Join(configDir, "servers")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list server files: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths, nil
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

func (s Servers) BrokenProjections() []SourceError {
	var broken []SourceError
	for _, sc := range s.Loaded {
		if sc.ProjectionsErr != nil {
			broken = append(broken, *sc.ProjectionsErr)
		}
	}
	return broken
}

func (s Servers) Problems() []SourceError {
	return slices.Concat(s.Broken, s.BrokenProjections())
}

func (s Servers) HasProblems() bool {
	return len(s.Problems()) > 0
}

func (s Servers) IsEnabled(name string) bool {
	sc, ok := s.Find(name)
	return ok && sc.IsEnabled()
}

func loadServerFile(configDir, path string) (ServerConfig, error) {
	sc, err := loadServerConfig(path)
	if err != nil {
		return ServerConfig{}, err
	}
	loadProjections(sc, path, ProjectionPath(configDir, sc.Name))
	mergeKnownAuth(configDir, sc)
	return *sc, nil
}

// A broken projection leaves the server loaded without projections rather than broken: the server
// file alone decides whether and how mini connects.
func loadProjections(sc *ServerConfig, serverPath, projectionPath string) {
	if sc.ProjectionsErr != nil {
		return
	}
	failed := func(path string, err error) {
		sc.Projections = nil
		sc.ProjectionsErr = &SourceError{Path: path, ServerName: sc.Name, Err: err}
	}
	if err := overlayProjectionFile(sc, projectionPath); err != nil {
		failed(projectionPath, err)
		return
	}
	// The overlay already checked the projection file's own rules, so only an inline rule can fail here.
	if err := validateServerProjectionFormats(sc.Name, sc.Projections); err != nil {
		failed(serverPath, fmt.Errorf("%s: %w", serverPath, err))
	}
}

func overlayProjectionFile(sc *ServerConfig, path string) error {
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
	if err := validateServerProjectionFormats(sc.Name, toolProjections); err != nil {
		return fmt.Errorf("%s: %w", path, err)
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

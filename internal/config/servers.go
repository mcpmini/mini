package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	for _, path := range paths {
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

func LoadServer(configDir, name string) (ServerConfig, error) {
	path, err := existingServerPath(configDir, name)
	if err != nil {
		return ServerConfig{}, err
	}
	return loadServerFile(configDir, path)
}

func existingServerPath(configDir, name string) (string, error) {
	if err := checkServerName(name, "the request"); err != nil {
		return "", err
	}
	path := ServerPath(configDir, name)
	if !ServerFileExists(configDir, name) {
		return "", fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return path, nil
}

func loadServerFile(configDir, path string) (ServerConfig, error) {
	sc, err := loadServerConfig(path)
	if err != nil {
		return ServerConfig{}, err
	}
	mergeKnownAuth(configDir, sc)
	return *sc, nil
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

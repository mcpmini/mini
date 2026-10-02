package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceError is a server file that failed to load.
type SourceError struct {
	Path       string
	ServerName string
	Err        error
}

type LoadProjectionsResult struct {
	Projections    map[string]map[string]*ProjectionConfig
	SkippedServers map[string]error
	SourceErrors   []SourceError
}

func (l LoadProjectionsResult) KeepsPreviousProjection(name string) bool {
	if _, ok := l.SkippedServers[name]; ok {
		return true
	}
	return sourceFailed(l.SourceErrors, name)
}

func sourceFailed(errs []SourceError, name string) bool {
	for _, se := range errs {
		if se.ServerName == name {
			return true
		}
	}
	return false
}

func newLoadProjectionsResult() LoadProjectionsResult {
	return LoadProjectionsResult{
		Projections:    make(map[string]map[string]*ProjectionConfig),
		SkippedServers: make(map[string]error),
	}
}

func LoadProjections(configDir string) LoadProjectionsResult {
	load := newLoadProjectionsResult()
	servers := loadServerDirLenient(configDir, &load)
	projFiles := loadProjFilesIsolated(configDir, servers, &load)
	mergeProjections(servers, projFiles)
	extractAndValidateProjections(servers, &load)
	return load
}

func loadServerDirLenient(configDir string, load *LoadProjectionsResult) []ServerConfig {
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml"))
	var out []ServerConfig
	for _, p := range filterServerPaths(paths) {
		s, err := loadServerConfigLenient(p)
		if err != nil {
			load.SourceErrors = append(load.SourceErrors, SourceError{Path: p, ServerName: serverNameFromPath(p), Err: err})
			continue
		}
		out = append(out, *s)
	}
	return out
}

func loadServerConfigLenient(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseServerConfig(path, data, lenientEnvExpansion)
}

func loadProjFilesIsolated(configDir string, servers []ServerConfig, load *LoadProjectionsResult) map[string]map[string]*ProjectionConfig {
	configured := make(map[string]bool, len(servers))
	for _, s := range servers {
		configured[s.Name] = true
	}
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.proj.yaml"))
	projFiles := make(map[string]map[string]*ProjectionConfig)
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".proj.yaml")
		if !configured[name] {
			continue
		}
		if err := loadOneProjectionFile(projFiles, p); err != nil {
			load.SkippedServers[name] = err
		}
	}
	return projFiles
}

func extractAndValidateProjections(servers []ServerConfig, load *LoadProjectionsResult) {
	for _, s := range servers {
		if _, skip := load.SkippedServers[s.Name]; skip {
			continue
		}
		if s.Projections != nil {
			load.Projections[s.Name] = s.Projections
		}
	}
	validateProjectionsIsolated(servers, load)
}

func validateProjectionsIsolated(servers []ServerConfig, load *LoadProjectionsResult) {
	for _, s := range servers {
		if err := validateLoadedProjections(s.Name, load.Projections[s.Name]); err != nil {
			load.SkippedServers[s.Name] = err
			delete(load.Projections, s.Name)
		}
	}
}

func validateLoadedProjections(name string, projections map[string]*ProjectionConfig) error {
	if err := validateServerProjectionFormats(name, projections); err != nil {
		return err
	}
	return nil
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

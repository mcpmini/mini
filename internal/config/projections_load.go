package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SourceError is a config file that failed to load, so its server names cannot be trusted.
type SourceError struct {
	Path string
	Err  error
}

type LoadProjectionsResult struct {
	Projections    map[string]map[string]*ProjectionConfig
	SkippedServers map[string]error
	SourceErrors   []SourceError
	freshLoaded    map[string]bool
}

func (l LoadProjectionsResult) KeepsPreviousProjection(name string) bool {
	if _, ok := l.SkippedServers[name]; ok {
		return true
	}
	return len(l.SourceErrors) > 0 && !l.freshLoaded[name]
}

func LoadProjections(configDir string) LoadProjectionsResult {
	load := LoadProjectionsResult{
		Projections:    make(map[string]map[string]*ProjectionConfig),
		SkippedServers: make(map[string]error),
		freshLoaded:    make(map[string]bool),
	}
	servers := loadLenientServers(configDir, &load)
	projFiles := loadProjFilesIsolated(configDir, servers, &load)
	mergeProjections(servers, projFiles)
	extractAndValidateProjections(servers, &load)
	return load
}

func loadLenientServers(configDir string, load *LoadProjectionsResult) []ServerConfig {
	fileServers := loadServerDirLenient(configDir, load)
	inlineServers := loadInlineServersLenient(configDir, load)
	combined := deduplicateServers(append(fileServers, inlineServers...))
	markFreshLoaded(combined, fileServers, load)
	return combined
}

func markFreshLoaded(combined, fileServers []ServerConfig, load *LoadProjectionsResult) {
	fromServerFile := make(map[string]bool, len(fileServers))
	for _, s := range fileServers {
		fromServerFile[s.Name] = true
	}
	shadowingFileMayHaveFailed := len(load.SourceErrors) > 0
	for _, s := range combined {
		if fromServerFile[s.Name] || !shadowingFileMayHaveFailed {
			load.freshLoaded[s.Name] = true
		}
	}
}

func loadServerDirLenient(configDir string, load *LoadProjectionsResult) []ServerConfig {
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml"))
	var out []ServerConfig
	for _, p := range filterServerPaths(paths) {
		s, err := loadServerConfigLenient(p)
		if err != nil {
			load.SourceErrors = append(load.SourceErrors, SourceError{Path: p, Err: err})
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
	return parseServerConfig(path, interpolateEnvMarkingUndefined(data))
}

func loadInlineServersLenient(configDir string, load *LoadProjectionsResult) []ServerConfig {
	configPath := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		load.SourceErrors = append(load.SourceErrors, SourceError{Path: configPath, Err: fmt.Errorf("read config.yaml: %w", err)})
		return nil
	}
	var cfg Config
	if err := yaml.Unmarshal(interpolateEnvMarkingUndefined(data), &cfg); err != nil {
		load.SourceErrors = append(load.SourceErrors, SourceError{Path: configPath, Err: fmt.Errorf("parse config.yaml: %w", err)})
		return nil
	}
	if err := validateInlineServers(configPath, cfg.Servers); err != nil {
		load.SourceErrors = append(load.SourceErrors, SourceError{Path: configPath, Err: err})
		return nil
	}
	return cfg.Servers
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
	return rejectUndefinedEnvInProjections(name, projections)
}

func rejectUndefinedEnvInProjections(name string, projections map[string]*ProjectionConfig) error {
	data, err := yaml.Marshal(projections)
	if err != nil {
		return fmt.Errorf("server %s: projections: %w", name, err)
	}
	if m := undefinedEnvMarkerRef.FindSubmatch(data); m != nil {
		return fmt.Errorf("server %s: projections reference undefined environment variable %s", name, m[1])
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

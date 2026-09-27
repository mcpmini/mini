package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectionsLoad is the result of a per-server-isolated projection load.
// Projections contains servers whose sources all loaded successfully.
// Skipped contains server names that had at least one failure; callers keep
// the previous live projections for those names.
type ProjectionsLoad struct {
	Projections      map[string]map[string]*ProjectionConfig
	Skipped          map[string]error
	fromServerFiles  map[string]bool
	mainConfigFailed bool
}

// KeepsPrevious reports whether the caller should retain the previous live
// projection for name rather than applying the freshly loaded one.
func (l ProjectionsLoad) KeepsPrevious(name string) bool {
	if _, ok := l.Skipped[name]; ok {
		return true
	}
	return l.mainConfigFailed && !l.fromServerFiles[name]
}

type minimalServer struct {
	Name        string                       `yaml:"name"`
	Projections map[string]*ProjectionConfig `yaml:"projections,omitempty"`
}

type minimalMainConfig struct {
	Servers []minimalServer `yaml:"servers,omitempty"`
}

// LoadProjections reads projection-relevant data only — no env interpolation,
// no transport validation — so a ${VAR} in an unrelated field never blocks a
// good server's projections from loading.
func LoadProjections(configDir string) ProjectionsLoad {
	load := ProjectionsLoad{
		Projections:     make(map[string]map[string]*ProjectionConfig),
		Skipped:         make(map[string]error),
		fromServerFiles: make(map[string]bool),
	}
	configured := loadProjectionSources(configDir, &load)
	overlayProjFilesIsolated(configDir, configured, &load)
	validateProjectionFormatsIsolated(configured, &load)
	return load
}

func loadProjectionSources(configDir string, load *ProjectionsLoad) []minimalServer {
	fromFiles := loadServerDirMinimal(configDir, load)
	inlines, failed := loadInlineMinimalServers(configDir)
	load.mainConfigFailed = failed

	fileNames := make(map[string]bool, len(fromFiles))
	for _, s := range fromFiles {
		fileNames[s.Name] = true
	}
	combined := fromFiles
	for _, s := range inlines {
		if !fileNames[s.Name] {
			combined = append(combined, s)
		}
	}
	for _, s := range combined {
		if _, skip := load.Skipped[s.Name]; skip {
			continue
		}
		if s.Projections != nil {
			load.Projections[s.Name] = s.Projections
		}
	}
	return combined
}

func loadServerDirMinimal(configDir string, load *ProjectionsLoad) []minimalServer {
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml"))
	var out []minimalServer
	for _, p := range filterServerPaths(paths) {
		stem := strings.TrimSuffix(filepath.Base(p), ".yaml")
		s, err := readMinimalServer(p)
		if err != nil {
			load.Skipped[stem] = err
			continue
		}
		if !ValidServerName.MatchString(s.Name) {
			load.Skipped[stem] = fmt.Errorf("invalid server name %q in %s", s.Name, p)
			continue
		}
		load.fromServerFiles[s.Name] = true
		out = append(out, s)
	}
	return out
}

func readMinimalServer(path string) (minimalServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return minimalServer{}, fmt.Errorf("read %s: %w", path, err)
	}
	var s minimalServer
	if err := yaml.Unmarshal(data, &s); err != nil {
		return minimalServer{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func loadInlineMinimalServers(configDir string) ([]minimalServer, bool) {
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		return nil, true
	}
	var cfg minimalMainConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, true
	}
	return cfg.Servers, false
}

func overlayProjFilesIsolated(configDir string, configured []minimalServer, load *ProjectionsLoad) {
	configuredSet := make(map[string]bool, len(configured))
	for _, s := range configured {
		configuredSet[s.Name] = true
	}
	paths, _ := filepath.Glob(filepath.Join(configDir, "servers", "*.proj.yaml"))
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".proj.yaml")
		if !configuredSet[name] {
			continue
		}
		if _, skip := load.Skipped[name]; skip {
			continue
		}
		applyOneProjFileIsolated(p, name, load)
	}
}

func applyOneProjFileIsolated(p, name string, load *ProjectionsLoad) {
	tmp := make(map[string]map[string]*ProjectionConfig)
	if err := loadOneProjectionFile(tmp, p); err != nil {
		load.Skipped[name] = err
		delete(load.Projections, name)
		return
	}
	tp, ok := tmp[name]
	if !ok {
		return
	}
	if load.Projections[name] == nil {
		load.Projections[name] = make(map[string]*ProjectionConfig)
	}
	for tool, pc := range tp {
		load.Projections[name][tool] = pc
	}
}

func validateProjectionFormatsIsolated(configured []minimalServer, load *ProjectionsLoad) {
	for _, s := range configured {
		if _, skip := load.Skipped[s.Name]; skip {
			continue
		}
		if err := validateServerProjectionFormats(s.Name, load.Projections[s.Name]); err != nil {
			load.Skipped[s.Name] = err
			delete(load.Projections, s.Name)
		}
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

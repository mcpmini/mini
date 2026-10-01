package config

import (
	"errors"
	"io/fs"
	"path/filepath"
)

// ServerSet is the server list as written on disk, loaded leniently so one bad
// file cannot hide the others.
type ServerSet struct {
	Servers      map[string]ServerConfig
	SourceErrors []SourceError
}

func LoadServerSet(configDir string) ServerSet {
	load := newLoadProjectionsResult()
	set := ServerSet{Servers: make(map[string]ServerConfig)}
	for _, sc := range loadServerDirLenient(configDir, &load) {
		set.Servers[sc.Name] = sc
	}
	set.SourceErrors = load.SourceErrors
	return set
}

// LoadLenient loads server files while tolerating broken sources. Callers that
// need global settings should use LoadMain.
func LoadLenient(configDir string) ([]ServerConfig, []SourceError) {
	load := newLoadProjectionsResult()
	servers := loadServerDirLenient(configDir, &load)
	mergeKnownAuth(configDir, servers)
	return servers, load.SourceErrors
}

// LoadServer loads one server as Load would, without needing every other server file to load.
func LoadServer(configDir, name string) (ServerConfig, error) {
	if err := checkServerName(name, "the request"); err != nil {
		return ServerConfig{}, err
	}
	sc, err := loadServerConfig(filepath.Join(configDir, "servers", name+".yaml"))
	if err != nil {
		return ServerConfig{}, err
	}
	projections := make(map[string]map[string]*ProjectionConfig)
	projPath := filepath.Join(configDir, "servers", name+".proj.yaml")
	if err := loadOneProjectionFile(projections, projPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ServerConfig{}, err
	}
	servers := []ServerConfig{*sc}
	mergeProjections(servers, projections)
	if err := validateServerProjectionFormats(name, servers[0].Projections); err != nil {
		return ServerConfig{}, err
	}
	mergeKnownAuth(configDir, servers)
	return servers[0], nil
}

func (set ServerSet) IsEnabled(name string) bool {
	sc, ok := set.Servers[name]
	return ok && sc.IsEnabled()
}

// KeepsPreviousServer reports whether name's server file failed to load.
func (set ServerSet) KeepsPreviousServer(name string) bool {
	return sourceFailed(set.SourceErrors, name)
}

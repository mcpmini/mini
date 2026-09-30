package config

import (
	"path/filepath"
	"slices"
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
	for _, sc := range loadLenientServers(configDir, &load) {
		set.Servers[sc.Name] = sc
	}
	set.SourceErrors = load.SourceErrors
	return set
}

// LoadLenient is Load for callers that must keep going when one file is broken.
func LoadLenient(configDir string) (*Config, []ServerConfig, []SourceError) {
	load := newLoadProjectionsResult()
	servers := loadLenientServers(configDir, &load)
	mergeKnownAuth(configDir, servers)
	cfg, err := loadMainConfig(configDir)
	if err != nil {
		path := filepath.Join(configDir, "config.yaml")
		if !slices.ContainsFunc(load.SourceErrors, func(e SourceError) bool { return e.Path == path }) {
			load.SourceErrors = append(load.SourceErrors, SourceError{Path: path, Err: err})
		}
		cfg = DefaultConfig()
	}
	return cfg, servers, load.SourceErrors
}

func (set ServerSet) IsEnabled(name string) bool {
	sc, ok := set.Servers[name]
	return ok && sc.IsEnabled()
}

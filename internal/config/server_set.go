package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ServerSet is the server list as written on disk, loaded leniently so one bad
// file cannot hide the others. Servers hold their settings before mergeKnownAuth and
// without projections: the daemon writes the OAuth marker itself, and projections reload separately.
type ServerSet struct {
	Servers      map[string]ServerConfig
	Skipped      map[string]error
	SourceErrors []SourceError
}

func LoadServerSet(configDir string) ServerSet {
	load := LoadProjectionsResult{
		Projections:    make(map[string]map[string]*ProjectionConfig),
		SkippedServers: make(map[string]error),
		freshLoaded:    make(map[string]bool),
	}
	set := ServerSet{Servers: make(map[string]ServerConfig), Skipped: make(map[string]error)}
	for _, sc := range loadLenientServers(configDir, &load) {
		sc.Projections = nil
		if err := checkServerSettingsLoaded(sc, load.freshLoaded[sc.Name]); err != nil {
			set.Skipped[sc.Name] = err
			continue
		}
		set.Servers[sc.Name] = sc
	}
	set.SourceErrors = load.SourceErrors
	return set
}

func checkServerSettingsLoaded(sc ServerConfig, fresh bool) error {
	if !fresh {
		return fmt.Errorf("server %s: the file that defines it failed to load", sc.Name)
	}
	data, err := yaml.Marshal(sc)
	if err != nil {
		return fmt.Errorf("server %s: %w", sc.Name, err)
	}
	if m := undefinedEnvMarkerRef.FindSubmatch(data); m != nil {
		return fmt.Errorf("server %s references undefined environment variable %s", sc.Name, m[1])
	}
	return nil
}

func SameServerSettings(a, b ServerConfig) bool {
	a.Projections, b.Projections = nil, nil
	aData, aErr := yaml.Marshal(a)
	bData, bErr := yaml.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aData, bData)
}

func WithKnownAuth(configDir string, sc ServerConfig) ServerConfig {
	servers := []ServerConfig{sc}
	mergeKnownAuth(configDir, servers)
	return servers[0]
}

package config

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

func (set ServerSet) IsEnabled(name string) bool {
	sc, ok := set.Servers[name]
	return ok && sc.IsEnabled()
}

// KeepsPreviousServer reports whether name's server file failed to load.
func (set ServerSet) KeepsPreviousServer(name string) bool {
	return sourceFailed(set.SourceErrors, name)
}

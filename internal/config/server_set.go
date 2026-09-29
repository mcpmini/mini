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
	for _, sc := range loadLenientServers(configDir, &load) {
		set.Servers[sc.Name] = sc
	}
	set.SourceErrors = load.SourceErrors
	return set
}

func (set ServerSet) IsEnabled(name string) bool {
	sc, ok := set.Servers[name]
	return ok && sc.IsEnabled()
}

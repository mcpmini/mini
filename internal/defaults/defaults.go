package defaults

import (
	"embed"
)

//go:embed projections/*.yaml permissions/*.yaml auth/*.yaml
var FS embed.FS

// ProjectionFor returns the bundled projection YAML for the given server name,
// or nil if no bundled config exists for that name.
func ProjectionFor(serverName string) []byte {
	data, err := FS.ReadFile("projections/" + serverName + ".yaml")
	if err != nil {
		return nil
	}
	return data
}

// PermissionsFor returns the bundled permissions YAML for the given server name,
// or nil if no bundled defaults exist for that name.
func PermissionsFor(serverName string) []byte {
	data, err := FS.ReadFile("permissions/" + serverName + ".yaml")
	if err != nil {
		return nil
	}
	return data
}

// AuthFor returns the bundled auth config YAML for the given server name,
// or nil if no bundled defaults exist for that name.
func AuthFor(serverName string) []byte {
	data, err := FS.ReadFile("auth/" + serverName + ".yaml")
	if err != nil {
		return nil
	}
	return data
}

// HasBundledAuth reports whether a bundled auth default exists for the given
// server URL. When true, writing an explicit auth block in the server YAML
// would shadow the bundled config: config.mergeKnownAuth only applies bundled
// auth when no Auth block is present.
func HasBundledAuth(url string) bool {
	key := DetectKey("", url)
	return key != "" && AuthFor(key) != nil
}

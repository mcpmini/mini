package defaults

import "embed"

//go:embed projections/*.yaml permissions/*.yaml
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

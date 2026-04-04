package defaults

import "embed"

// FS holds the bundled default projection configs shipped with the binary.
// Files are keyed as "projections/<servername>.yaml".
//
//go:embed projections/*.yaml
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

package defaults

import "embed"

//go:embed projections/*.yaml permissions/*.yaml auth/*.yaml
var FS embed.FS

// ProjectionFor returns the bundled projection YAML for a MatchKnownServer key, or nil.
func ProjectionFor(key string) []byte {
	return bundledFile("projections", key)
}

// PermissionsFor returns the bundled permissions YAML for a MatchKnownServer key, or nil.
func PermissionsFor(key string) []byte {
	return bundledFile("permissions", key)
}

// AuthFor returns the bundled auth YAML for a MatchKnownServer key, or nil.
func AuthFor(key string) []byte {
	return bundledFile("auth", key)
}

func bundledFile(dir, key string) []byte {
	data, err := FS.ReadFile(dir + "/" + key + ".yaml")
	if err != nil {
		return nil
	}
	return data
}

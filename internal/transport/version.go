package transport

import "slices"

var LegacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

func IsLegacyVersion(v string) bool {
	return slices.Contains(LegacyVersions, v)
}

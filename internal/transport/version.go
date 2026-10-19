package transport

import "slices"

// LatestLegacyVersion is the version mini offers in initialize, so it must stay the newest legacy version.
const LatestLegacyVersion = "2025-11-25"

var LegacyVersions = []string{LatestLegacyVersion, "2025-06-18", "2025-03-26", "2024-11-05"}

func IsLegacyVersion(v string) bool {
	return slices.Contains(LegacyVersions, v)
}

package transport

import "slices"

type Era uint8

const (
	EraLegacy Era = iota
	EraModern
)

// LatestLegacyVersion is the version mini offers in initialize, so it must stay the newest legacy version.
const LatestLegacyVersion = "2025-11-25"

var LegacyVersions = []string{LatestLegacyVersion, "2025-06-18", "2025-03-26", "2024-11-05"}

var ModernVersions = []string{"2026-07-28"}

func IsLegacyVersion(v string) bool {
	return slices.Contains(LegacyVersions, v)
}

func IsModernVersion(v string) bool {
	return slices.Contains(ModernVersions, v)
}

func SupportedVersions() []string {
	return slices.Concat(ModernVersions, LegacyVersions)
}

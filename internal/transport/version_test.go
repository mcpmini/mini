package transport

import "testing"

func TestIsLegacyVersion(t *testing.T) {
	for _, version := range LegacyVersions {
		if !IsLegacyVersion(version) {
			t.Errorf("IsLegacyVersion(%q) = false, want true", version)
		}
	}
	for _, version := range []string{"1999-01-01", "", "2026-07-28"} {
		if IsLegacyVersion(version) {
			t.Errorf("IsLegacyVersion(%q) = true, want false", version)
		}
	}
}

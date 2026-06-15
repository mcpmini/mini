package version

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	tests := []struct {
		name     string
		info     *debug.BuildInfo
		expected string
	}{
		{
			name:     "no vcs settings",
			info:     &debug.BuildInfo{},
			expected: "dev",
		},
		{
			name: "clean commit",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			expected: "abc1234",
		},
		{
			name: "dirty commit",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			expected: "abc1234+dirty",
		},
		{
			name: "tagged release clean",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			expected: "v1.2.3 (abc1234)",
		},
		{
			name: "tagged release dirty",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			expected: "v1.2.3 (abc1234+dirty)",
		},
		{
			name: "pseudo-version skipped",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.1.1-0.20260608050802-758ae7eb6151"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			expected: "abc1234",
		},
		{
			name: "devel module version skipped",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def5678901234"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			expected: "abc1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildVersion(tt.info)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

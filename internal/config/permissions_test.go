package config

import "testing"

func TestPermissionsLevelFor(t *testing.T) {
	tests := []struct {
		name     string
		perm     *PermissionsConfig
		toolName string
		want     PermissionLevel
	}{
		{
			name:     "nil permissions default open",
			perm:     nil,
			toolName: "anything",
			want:     PermOpen,
		},
		{
			name: "hidden match wins case-insensitively",
			perm: &PermissionsConfig{
				Default:   string(PermOpen),
				Hidden:    []string{"DeleteRepo"},
				Protected: []string{"DeleteRepo"},
			},
			toolName: "deleterepo",
			want:     PermHidden,
		},
		{
			name: "protected match wins case-insensitively",
			perm: &PermissionsConfig{
				Default:   string(PermOpen),
				Protected: []string{"AdminTool"},
			},
			toolName: "admintool",
			want:     PermProtected,
		},
		{
			name: "hidden default is applied",
			perm: &PermissionsConfig{
				Default: string(PermHidden),
			},
			toolName: "anything",
			want:     PermHidden,
		},
		{
			name: "protected default is applied",
			perm: &PermissionsConfig{
				Default: string(PermProtected),
			},
			toolName: "anything",
			want:     PermProtected,
		},
		{
			name:     "empty default remains open",
			perm:     &PermissionsConfig{},
			toolName: "anything",
			want:     PermOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.perm.LevelFor(tt.toolName); got != tt.want {
				t.Fatalf("LevelFor(%q) = %q, want %q", tt.toolName, got, tt.want)
			}
		})
	}
}

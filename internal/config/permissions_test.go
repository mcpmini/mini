package config

import "testing"

func TestPermissionsHides(t *testing.T) {
	tests := []struct {
		name     string
		perm     *PermissionsConfig
		toolName string
		want     bool
	}{
		{name: "nil permissions hide nothing", perm: nil, toolName: "anything", want: false},
		{name: "empty permissions hide nothing", perm: &PermissionsConfig{}, toolName: "anything", want: false},
		{
			name:     "listed tool is hidden case-insensitively",
			perm:     &PermissionsConfig{Hidden: []string{"DeleteRepo"}},
			toolName: "deleterepo",
			want:     true,
		},
		{
			name:     "unlisted tool stays visible",
			perm:     &PermissionsConfig{Hidden: []string{"DeleteRepo"}},
			toolName: "getRepo",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.perm.Hides(tt.toolName); got != tt.want {
				t.Fatalf("Hides(%q) = %t, want %t", tt.toolName, got, tt.want)
			}
		})
	}
}

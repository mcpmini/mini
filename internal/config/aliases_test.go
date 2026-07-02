package config_test

import (
	"maps"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestAliasesFromProjections(t *testing.T) {
	tests := []struct {
		name string
		proj map[string]*config.ProjectionConfig
		want map[string]string
	}{
		{name: "nil input", proj: nil, want: nil},
		{name: "empty input", proj: map[string]*config.ProjectionConfig{}, want: nil},
		{
			name: "single alias",
			proj: map[string]*config.ProjectionConfig{
				"list_pull_requests": {Alias: "list_prs"},
			},
			want: map[string]string{"list_pull_requests": "list_prs"},
		},
		{
			name: "mixed aliased and non-aliased tools",
			proj: map[string]*config.ProjectionConfig{
				"list_pull_requests": {Alias: "list_prs"},
				"get_issue":          {IncludeOnly: []string{"id"}},
			},
			want: map[string]string{"list_pull_requests": "list_prs"},
		},
		{
			name: "nil projection entry is skipped",
			proj: map[string]*config.ProjectionConfig{
				"get_issue": nil,
			},
			want: nil,
		},
		{
			name: "no tool defines an alias",
			proj: map[string]*config.ProjectionConfig{
				"get_issue": {IncludeOnly: []string{"id"}},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.AliasesFromProjections(tt.proj)
			if !maps.Equal(got, tt.want) {
				t.Errorf("AliasesFromProjections(%v) = %v, want %v", tt.proj, got, tt.want)
			}
		})
	}
}

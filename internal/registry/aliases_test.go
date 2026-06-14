package registry_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/registry"
)

func TestResolveAliases(t *testing.T) {
	tests := []struct {
		name            string
		realNames       []string
		aliasByToolName map[string]string
		wantAlias       map[string]string // real name → expected alias ("" = none)
		wantDropped     []string          // real names expected to be marked dropped
	}{
		{
			name:      "no alias defined",
			realNames: []string{"my_tool"},
			wantAlias: map[string]string{"my_tool": ""},
		},
		{
			name:            "valid alias accepted",
			realNames:       []string{"list_pull_requests"},
			aliasByToolName: map[string]string{"list_pull_requests": "list_prs"},
			wantAlias:       map[string]string{"list_pull_requests": "list_prs"},
		},
		{
			name:            "invalid alias chars ignored, not dropped",
			realNames:       []string{"my_tool"},
			aliasByToolName: map[string]string{"my_tool": "bad alias!"},
			wantAlias:       map[string]string{"my_tool": ""},
		},
		{
			name:            "alias collides with real tool name",
			realNames:       []string{"toolA", "toolB"},
			aliasByToolName: map[string]string{"toolA": "toolB"},
			wantAlias:       map[string]string{"toolA": "", "toolB": ""},
			wantDropped:     []string{"toolA"},
		},
		{
			name:            "two aliases claim the same visible name — both dropped",
			realNames:       []string{"toolA", "toolB"},
			aliasByToolName: map[string]string{"toolA": "shared", "toolB": "shared"},
			wantAlias:       map[string]string{"toolA": "", "toolB": ""},
			wantDropped:     []string{"toolA", "toolB"},
		},
		{
			name:      "nil alias map is safe",
			realNames: []string{"my_tool"},
			wantAlias: map[string]string{"my_tool": ""},
		},
		{
			name:            "unaliased tools unaffected by collision elsewhere",
			realNames:       []string{"toolA", "toolB", "toolC"},
			aliasByToolName: map[string]string{"toolA": "shared", "toolB": "shared"},
			wantAlias:       map[string]string{"toolA": "", "toolB": "", "toolC": ""},
			wantDropped:     []string{"toolA", "toolB"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := registry.ResolveAliases(tc.realNames, tc.aliasByToolName)

			for realName, want := range tc.wantAlias {
				if got := res.AliasFor(realName); got != want {
					t.Errorf("AliasFor(%q): want %q, got %q", realName, want, got)
				}
			}

			dropped := make(map[string]bool, len(tc.wantDropped))
			for _, n := range tc.wantDropped {
				dropped[n] = true
			}
			for realName := range tc.wantAlias {
				if got, want := res.WasDropped(realName), dropped[realName]; got != want {
					t.Errorf("WasDropped(%q): want %v, got %v", realName, want, got)
				}
			}
		})
	}
}

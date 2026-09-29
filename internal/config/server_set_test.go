//go:build test

package config_test

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestLoadServerSet(t *testing.T) {
	cases := []struct {
		name             string
		files            map[string]string
		wantServers      []string
		wantSkipped      []string
		wantSourceErrors int
	}{
		{
			name:        "file and inline servers are both loaded",
			files:       map[string]string{"servers/a.yaml": "name: a\ncommand: echo\n", "config.yaml": "servers:\n- name: b\n  command: echo\n"},
			wantServers: []string{"a", "b"},
		},
		{
			name:             "a malformed file is a source error and does not hide the others",
			files:            map[string]string{"servers/a.yaml": "name: a\ncommand: [oops\n", "servers/b.yaml": "name: b\ncommand: echo\n"},
			wantServers:      []string{"b"},
			wantSourceErrors: 1,
		},
		{
			name: "an inline entry never stands in for a file-defined server whose file failed",
			files: map[string]string{
				"servers/svc.yaml": "name: svc\ncommand: [oops\n",
				"config.yaml":      "servers:\n- name: svc\n  command: echo\n",
			},
			wantSkipped:      []string{"svc"},
			wantSourceErrors: 1,
		},
		{
			name:        "an undefined variable in a connection setting skips the server",
			files:       map[string]string{"servers/svc.yaml": "name: svc\ncommand: echo\nenv: [TOKEN=${SERVER_SET_UNDEFINED_XYZ}]\n"},
			wantSkipped: []string{"svc"},
		},
		{
			name:        "an undefined variable only in projections does not skip the server",
			files:       map[string]string{"servers/svc.yaml": "name: svc\ncommand: echo\nprojections:\n  t:\n    exclude: [${SERVER_SET_UNDEFINED_XYZ}]\n"},
			wantServers: []string{"svc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}

			set := config.LoadServerSet(dir)

			if got := slices.Sorted(maps.Keys(set.Servers)); !slices.Equal(got, tc.wantServers) {
				t.Errorf("servers = %v, want %v", got, tc.wantServers)
			}
			if got := slices.Sorted(maps.Keys(set.Skipped)); !slices.Equal(got, tc.wantSkipped) {
				t.Errorf("skipped = %v, want %v", got, tc.wantSkipped)
			}
			if len(set.SourceErrors) != tc.wantSourceErrors {
				t.Errorf("source errors = %d, want %d: %v", len(set.SourceErrors), tc.wantSourceErrors, set.SourceErrors)
			}
		})
	}
}

func TestLoadServerSet_settingsAreComparableAcrossDaemonWrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers/svc.yaml"), "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [a]\n")
	if err := config.MarkOAuthDetected(dir, "svc"); err != nil {
		t.Fatal(err)
	}

	svc := config.LoadServerSet(dir).Servers["svc"]

	if svc.Projections != nil {
		t.Errorf("projections = %v, want none: projection edits must not look like settings changes", svc.Projections)
	}
	if svc.Auth != nil {
		t.Errorf("auth = %+v, want none: the daemon's own OAuth marker must not look like a user edit", svc.Auth)
	}
}

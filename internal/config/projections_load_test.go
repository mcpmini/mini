//go:build test

package config_test

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

type projLoadCase struct {
	name              string
	files             map[string]string
	env               map[string]string
	wantProjected     []string
	wantAbsent        []string
	wantSkipped       []string // nil = don't check; []string{} = assert empty
	wantSourceErrors  int
	wantKeepsPrevious []string
	wantFresh         []string
	check             func(*testing.T, string, config.LoadProjectionsResult)
}

func checkProjLoad(t *testing.T, dir string, load config.LoadProjectionsResult, tc projLoadCase) {
	t.Helper()
	for _, name := range tc.wantProjected {
		if load.Projections[name] == nil {
			t.Errorf("expected %q in Projections", name)
		}
	}
	for _, name := range tc.wantAbsent {
		if _, ok := load.Projections[name]; ok {
			t.Errorf("expected %q absent from Projections", name)
		}
	}
	if tc.wantSkipped != nil {
		for _, name := range tc.wantSkipped {
			if _, ok := load.SkippedServers[name]; !ok {
				t.Errorf("expected %q in SkippedServers", name)
			}
		}
		if len(tc.wantSkipped) == 0 && len(load.SkippedServers) != 0 {
			t.Errorf("expected SkippedServers empty, got %v", load.SkippedServers)
		}
	}
	if got := len(load.SourceErrors); got != tc.wantSourceErrors {
		t.Errorf("expected %d SourceErrors, got %d: %v", tc.wantSourceErrors, got, load.SourceErrors)
	}
	for _, name := range tc.wantKeepsPrevious {
		if !load.KeepsPreviousProjection(name) {
			t.Errorf("KeepsPreviousProjection(%q) should be true", name)
		}
	}
	for _, name := range tc.wantFresh {
		if load.KeepsPreviousProjection(name) {
			t.Errorf("KeepsPreviousProjection(%q) should be false", name)
		}
	}
	if tc.check != nil {
		tc.check(t, dir, load)
	}
}

func TestLoadProjections(t *testing.T) {
	cases := []projLoadCase{
		{
			name:        "orphan proj.yaml is ignored",
			files:       map[string]string{"servers/orphan.proj.yaml": "tool:\n  include_only: [x]\n"},
			wantAbsent:  []string{"orphan"},
			wantSkipped: []string{},
		},
		{
			name:        "broken orphan proj.yaml is ignored: no SkippedServers, no SourceError",
			files:       map[string]string{"servers/orphan.proj.yaml": "bad: [yaml\n"},
			wantSkipped: []string{},
		},
		{
			name:          "undefined ${VAR} in server file still loads projections",
			files:         map[string]string{"servers/svc.yaml": "name: svc\nurl: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_TOKEN_XYZ}\nprojections:\n  t:\n    include_only: [a]\n"},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
		},
		{
			name:             "server name stays literal and invalid name is a source error",
			files:            map[string]string{"servers/svc.yaml": "name: ${PROJ_DEFINED_NAME_XYZ}\ncommand: echo\n"},
			wantSourceErrors: 1,
		},
		{
			name:             "args ${VAR} is an unexpanded connection field source error",
			files:            map[string]string{"servers/svc.yaml": "name: svc\ncommand: echo\nargs: [--token, \"${PROJ_TEST_TOK_XYZ}\"]\nprojections:\n  t:\n    include_only: [a]\n"},
			wantSourceErrors: 1,
		},
		{
			name:          "undefined ${VAR} in inline projection rule stays literal",
			files:         map[string]string{"config.yaml": "servers:\n- name: svc\n  command: echo\n  projections:\n    t:\n      exclude: [\"${PROJ_UNDEFINED_FIELD_XYZ}\"]\n"},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if got := load.Projections["svc"]["t"].Exclude[0]; got != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("exclude = %q, want literal reference", got)
				}
			},
		},
		{
			name:          "undefined ${VAR} in server file projection rule stays literal",
			files:         map[string]string{"servers/svc.yaml": "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [\"${PROJ_UNDEFINED_FIELD_XYZ}\"]\n"},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if got := load.Projections["svc"]["t"].IncludeOnly[0]; got != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("include_only = %q, want literal reference", got)
				}
			},
		},
		{
			name:             "server name stays literal and invalid name is a source error",
			files:            map[string]string{"servers/x.yaml": "name: ${PROJ_UNDEFINED_NAME_XYZ}\ncommand: echo\nprojections:\n  t:\n    include_only: [a]\n"},
			wantSourceErrors: 1,
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if len(load.Projections) != 0 {
					t.Errorf("no server should load from an undefined name, got %v", load.Projections)
				}
			},
		},
		{
			name:              "inline server name stays literal and invalid name is a config.yaml source error",
			files:             map[string]string{"config.yaml": "servers:\n- name: svc\n  command: echo\n  projections:\n    t:\n      include_only: [a]\n- name: ${PROJ_UNDEFINED_INLINE_NAME_XYZ}\n  command: echo\n"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"svc"},
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if len(load.Projections) != 0 {
					t.Errorf("no server should load from an invalid inline name, got %v", load.Projections)
				}
			},
		},
		{
			name:              "invalid inline handshake_timeout: config.yaml is a source error, as in config.Load",
			files:             map[string]string{"config.yaml": "servers:\n- name: svc\n  command: echo\n  handshake_timeout: invalid\n  projections:\n    t:\n      include_only: [a]\n"},
			wantAbsent:        []string{"svc"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"svc"},
		},
		{
			name:        "file stem differs from name: bad format → SkippedServers by real name",
			files:       map[string]string{"servers/github-server.yaml": "name: github\ncommand: echo\nprojections:\n  t:\n    format: bad-format\n"},
			wantAbsent:  []string{"github"},
			wantSkipped: []string{"github"},
		},
		{
			name: "broken servers/ file: inline twin not treated as fresh",
			files: map[string]string{
				"servers/github.yaml": "bad: [yaml\n",
				"config.yaml":         "servers:\n- name: github\n  command: echo\n",
			},
			wantAbsent:        []string{"github"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"github"},
		},
		{
			name: "bad .proj.yaml with inline twin: server absent from Projections",
			files: map[string]string{
				"servers/b.yaml":      "name: b\ncommand: echo\nprojections:\n  t:\n    include_only: [inline]\n",
				"servers/b.proj.yaml": "bad: [yaml\n",
			},
			wantAbsent:        []string{"b"},
			wantSkipped:       []string{"b"},
			wantKeepsPrevious: []string{"b"},
		},
		{
			name: "malformed servers/b.yaml: unattributable error, a still loads",
			files: map[string]string{
				"servers/a.yaml": "name: a\ncommand: echo\nprojections:\n  t:\n    include_only: [ok]\n",
				"servers/b.yaml": "bad: [yaml\n",
			},
			wantProjected:     []string{"a"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"b"},
			wantFresh:         []string{"a"},
		},
		{
			name: "invalid name in servers/ file: unattributable, others load",
			files: map[string]string{
				"servers/good.yaml":     "name: good\ncommand: echo\nprojections:\n  t:\n    include_only: [a]\n",
				"servers/bad-name.yaml": "name: invalid name!\ncommand: echo\n",
			},
			wantProjected:     []string{"good"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"someother"},
			wantFresh:         []string{"good"},
		},
		{
			name: "broken config.yaml: source error, inline names keep-previous, file names do not",
			files: map[string]string{
				"servers/file-svc.yaml": "name: file-svc\ncommand: echo\n",
				"config.yaml":           "bad: [yaml\n",
			},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"inline-only"},
			wantFresh:         []string{"file-svc"},
			check: func(t *testing.T, dir string, load config.LoadProjectionsResult) {
				if len(load.SourceErrors) > 0 && load.SourceErrors[0].Path != filepath.Join(dir, "config.yaml") {
					t.Errorf("expected SourceErrors to contain config.yaml path, got %v", load.SourceErrors)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			load := config.LoadProjections(dir)
			checkProjLoad(t, dir, load, tc)
		})
	}
}

func TestLoadProjections_parity(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		env   map[string]string
	}{
		{
			name: "multi-server with proj.yaml overlay and inline entries",
			files: map[string]string{
				"servers/a.yaml":      "name: a\nurl: https://a.example.com\nheaders:\n  Auth: Bearer ${PARITY_TOKEN}\nprojections:\n  tool1:\n    include_only: [x, y]\n  tool2:\n    exclude: [secret]\n",
				"servers/a.proj.yaml": "tool3:\n  include_only: [z]\n",
				"config.yaml":         "servers:\n- name: b\n  command: echo\n  projections:\n    toolB:\n      include_only: [q]\n",
			},
			env: map[string]string{"PARITY_TOKEN": "tok123"},
		},
		{
			name:  "defined ${VAR} in headers expands while projection reference stays literal",
			files: map[string]string{"servers/svc.yaml": "name: svc\nurl: https://x.example.com\nheaders:\n  Auth: Bearer ${PARITY_SVC_TOKEN}\nprojections:\n  t:\n    include_only: [\"${PARITY_SVC_FIELD}\"]\n"},
			env:   map[string]string{"PARITY_SVC_TOKEN": "tok", "PARITY_SVC_FIELD": "a"},
		},
		{
			name: "duplicate names: first wins; servers/ beats inline",
			files: map[string]string{
				"servers/a-svc.yaml": "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [first]\n",
				"servers/b-svc.yaml": "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [second]\n",
				"config.yaml":        "servers:\n- name: svc\n  command: echo\n  projections:\n    t:\n      include_only: [inline]\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			load := config.LoadProjections(dir)
			_, servers, err := config.Load(dir)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			for _, s := range servers {
				lp := load.Projections[s.Name]
				if len(s.Projections) == 0 && len(lp) == 0 {
					continue
				}
				if !reflect.DeepEqual(lp, s.Projections) {
					t.Errorf("parity mismatch for %s:\n  LoadProjections: %v\n  config.Load: %v",
						s.Name, projKeys(lp), projKeys(s.Projections))
				}
			}
		})
	}
}

func projKeys(m map[string]*config.ProjectionConfig) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestLoadProjections_projFilesSourceError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), "name: svc\ncommand: echo\n")
	p := filepath.Join(dir, "servers", "svc.proj.yaml")
	if err := os.WriteFile(p, []byte("tool:\n  include_only: [a]\n"), 0000); err != nil {
		t.Skip("cannot create unreadable file:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0600) })
	if os.Getuid() == 0 {
		t.Skip("running as root; permission test not meaningful")
	}

	load := config.LoadProjections(dir)

	if _, ok := load.SkippedServers["svc"]; !ok {
		t.Error("unreadable .proj.yaml should cause svc to be in SkippedServers")
	}
	if !load.KeepsPreviousProjection("svc") {
		t.Error("KeepsPreviousProjection(svc) should be true after unreadable proj file")
	}
	if _, ok := load.Projections["svc"]; ok {
		t.Error("svc should be absent from Projections after proj file error")
	}
}

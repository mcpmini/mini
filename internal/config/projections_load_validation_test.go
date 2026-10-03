//go:build test

package config_test

import (
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

type projLoadCase struct {
	name              string
	files             projectionSources
	env               map[string]string
	wantProjected     []string
	wantAbsent        []string
	wantSkipped       []string // nil = don't check; []string{} = assert empty
	wantSourceErrors  int
	wantKeepsPrevious []string
	wantDropsPrevious []string
	check             func(*testing.T, string, config.LoadProjectionsResult)
}

type projectionSources struct {
	Servers     []config.ServerConfig
	Projections []configtest.ProjectionFile
	RawFiles    map[string]string
}

func writeProjectionSources(t *testing.T, dir string, sources projectionSources) {
	t.Helper()
	for _, server := range sources.Servers {
		configtest.WriteServer(t, dir, server)
	}
	for _, projection := range sources.Projections {
		configtest.WriteProjections(t, dir, projection)
	}
	for path, content := range sources.RawFiles {
		testutil.WriteFile(t, filepath.Join(dir, path), content)
	}
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
	for _, name := range tc.wantDropsPrevious {
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
			name: "orphan proj.yaml is ignored",
			files: projectionSources{
				Projections: []configtest.ProjectionFile{
					{
						ServerName: "orphan",
						Tools: map[string]*config.ProjectionConfig{
							"tool": {
								IncludeOnly: []string{"x"},
							},
						},
					},
				},
			},
			wantAbsent:  []string{"orphan"},
			wantSkipped: []string{},
		},
		{
			name:        "broken orphan proj.yaml is ignored: no SkippedServers, no SourceError",
			files:       projectionSources{RawFiles: map[string]string{"servers/orphan.proj.yaml": "bad: [yaml\n"}},
			wantSkipped: []string{},
		},
		{
			name: "undefined ${VAR} in server file still loads projections",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "svc",
						URL:     "https://api.example.com",
						Headers: map[string]string{"Authorization": "Bearer ${UNDEFINED_TOKEN_XYZ}"},
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"a"},
							},
						},
					},
				},
			},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
		},
		{
			name: "args ${VAR} is an unexpanded connection field source error",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "svc",
						Command: "echo",
						Args:    []string{"--token", "${PROJ_TEST_TOK_XYZ}"},
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"a"},
							},
						},
					},
				},
			},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"svc"},
		},
		{
			name: "undefined ${VAR} in server file projection rule stays literal",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "svc",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"${PROJ_UNDEFINED_FIELD_XYZ}"},
							},
						},
					},
				},
			},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if got := load.Projections["svc"]["t"].IncludeOnly[0]; got != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("include_only = %q, want literal reference", got)
				}
			},
		},
		{
			name: "bad projection format skips that server",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "github",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								Format: "bad-format",
							},
						},
					},
				},
			},
			wantAbsent:  []string{"github"},
			wantSkipped: []string{"github"},
		},
		{
			name: "bad .proj.yaml with rules in the server file: server absent from Projections",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "b",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"inline"},
							},
						},
					},
				},
				RawFiles: map[string]string{"servers/b.proj.yaml": "bad: [yaml\n"},
			},
			wantAbsent:        []string{"b"},
			wantSkipped:       []string{"b"},
			wantKeepsPrevious: []string{"b"},
		},
		{
			name: "malformed servers/b.yaml holds only b, not a or a server whose file is gone",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "a",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"ok"},
							},
						},
					},
				},
				RawFiles: map[string]string{"servers/b.yaml": "bad: [yaml\n"},
			},
			wantProjected:     []string{"a"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"b"},
			wantDropsPrevious: []string{"a", "file-gone"},
			check:             wantSourceErrorFor("b"),
		},
		{
			name: "invalid file name is a source error that holds no valid server",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "good",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"a"},
							},
						},
					},
				},
				RawFiles: map[string]string{"servers/bad.name.yaml": "command: echo\n"},
			},
			wantProjected:     []string{"good"},
			wantSourceErrors:  1,
			wantDropsPrevious: []string{"good"},
			check:             wantSourceErrorFor("bad.name"),
		},
		{
			name: "config.yaml is not a server source, even when broken",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "file-svc",
						Command: "echo",
					},
				},
				RawFiles: map[string]string{"config.yaml": "bad: [yaml\n"},
			},
			wantSourceErrors:  0,
			wantDropsPrevious: []string{"file-svc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProjectionSources(t, dir, tc.files)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			load := config.LoadProjections(dir)
			checkProjLoad(t, dir, load, tc)
		})
	}
}

func wantSourceErrorFor(name string) func(*testing.T, string, config.LoadProjectionsResult) {
	return func(t *testing.T, _ string, load config.LoadProjectionsResult) {
		t.Helper()
		if len(load.SourceErrors) != 1 || load.SourceErrors[0].ServerName != name {
			t.Errorf("SourceErrors = %v, want one for server %q", load.SourceErrors, name)
		}
	}
}

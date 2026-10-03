//go:build test

package config_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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
	wantDropsPrevious []string
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
			files:         map[string]string{"servers/svc.yaml": "url: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_TOKEN_XYZ}\nprojections:\n  t:\n    include_only: [a]\n"},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
		},
		{
			name:              "args ${VAR} is an unexpanded connection field source error",
			files:             map[string]string{"servers/svc.yaml": "command: echo\nargs: [--token, \"${PROJ_TEST_TOK_XYZ}\"]\nprojections:\n  t:\n    include_only: [a]\n"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"svc"},
		},
		{
			name:          "undefined ${VAR} in server file projection rule stays literal",
			files:         map[string]string{"servers/svc.yaml": "command: echo\nprojections:\n  t:\n    include_only: [\"${PROJ_UNDEFINED_FIELD_XYZ}\"]\n"},
			wantProjected: []string{"svc"},
			wantSkipped:   []string{},
			check: func(t *testing.T, _ string, load config.LoadProjectionsResult) {
				if got := load.Projections["svc"]["t"].IncludeOnly[0]; got != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("include_only = %q, want literal reference", got)
				}
			},
		},
		{
			name:        "bad projection format skips that server",
			files:       map[string]string{"servers/github.yaml": "command: echo\nprojections:\n  t:\n    format: bad-format\n"},
			wantAbsent:  []string{"github"},
			wantSkipped: []string{"github"},
		},
		{
			name: "bad .proj.yaml with rules in the server file: server absent from Projections",
			files: map[string]string{
				"servers/b.yaml":      "command: echo\nprojections:\n  t:\n    include_only: [inline]\n",
				"servers/b.proj.yaml": "bad: [yaml\n",
			},
			wantAbsent:        []string{"b"},
			wantSkipped:       []string{"b"},
			wantKeepsPrevious: []string{"b"},
		},
		{
			name: "malformed servers/b.yaml holds only b, not a or a server whose file is gone",
			files: map[string]string{
				"servers/a.yaml": "command: echo\nprojections:\n  t:\n    include_only: [ok]\n",
				"servers/b.yaml": "bad: [yaml\n",
			},
			wantProjected:     []string{"a"},
			wantSourceErrors:  1,
			wantKeepsPrevious: []string{"b"},
			wantDropsPrevious: []string{"a", "file-gone"},
			check:             wantSourceErrorFor("b"),
		},
		{
			name: "invalid file name is a source error that holds no valid server",
			files: map[string]string{
				"servers/good.yaml":     "command: echo\nprojections:\n  t:\n    include_only: [a]\n",
				"servers/bad.name.yaml": "command: echo\n",
			},
			wantProjected:     []string{"good"},
			wantSourceErrors:  1,
			wantDropsPrevious: []string{"good"},
			check:             wantSourceErrorFor("bad.name"),
		},
		{
			name: "config.yaml is not a server source, even when broken",
			files: map[string]string{
				"servers/file-svc.yaml": "command: echo\n",
				"config.yaml":           "bad: [yaml\n",
			},
			wantSourceErrors:  0,
			wantDropsPrevious: []string{"file-svc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				testutil.WriteFile(t, filepath.Join(dir, rel), []byte(content))
			}
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

func TestLoadServerSet_brokenFileHoldsOnlyItsServer(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "good.yaml"), []byte("command: echo\n"))
	if err := os.MkdirAll(filepath.Join(dir, "servers", "unreadable.yaml"), 0700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), []byte("bad: [yaml\n"))

	set := config.LoadServerSet(dir)

	if !set.IsEnabled("good") {
		t.Errorf("Servers = %v, want good loaded", set.Servers)
	}
	if !set.KeepsPreviousServer("unreadable") {
		t.Error("KeepsPreviousServer(unreadable) = false, want its running server kept")
	}
	for _, name := range []string{"good", "deleted"} {
		if set.KeepsPreviousServer(name) {
			t.Errorf("KeepsPreviousServer(%q) = true, want only the failing file's server held", name)
		}
	}
}

func TestLoadServer_matchesLoadWithoutNeedingTheOtherFiles(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "linear.yaml"), []byte("transport: http\nurl: https://mcp.linear.app/mcp\n"))
	testutil.WriteFile(t, filepath.Join(dir, "servers", "linear.proj.yaml"), []byte("list_issues:\n  include_only: [title]\n"))
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := *config.FindServer(servers, "linear")
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), []byte("bad: [yaml\n"))

	got, err := config.LoadServer(dir, "linear")

	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadServer = %+v\nwant what Load gives: %+v", got, want)
	}
	if got.Auth == nil || got.Projections["list_issues"] == nil {
		t.Errorf("LoadServer = %+v, want bundled auth and the projection file merged", got)
	}
	if _, err := config.LoadServer(dir, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(missing) err = %v, want fs.ErrNotExist", err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "servers", "linear.proj.yaml"), []byte("list_issues:\n  format: bogus\n"))
	if _, err := config.LoadServer(dir, "linear"); err == nil {
		t.Error("LoadServer accepted a projection format Load rejects, so the server would stop mini's next start")
	}
}

func TestLoadServer_aNameDifferingOnlyInCaseIsNotFound(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "github.yaml"), []byte("transport: http\nurl: https://api.githubcopilot.com/mcp/\n"))

	if sc, err := config.LoadServer(dir, "GitHub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(GitHub) = %q, %v; want fs.ErrNotExist, not github.yaml under another name", sc.Name, err)
	}
}

func TestLoadProjections_parity(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		env   map[string]string
	}{
		{
			name: "multi-server with proj.yaml overlay",
			files: map[string]string{
				"servers/a.yaml":      "url: https://a.example.com\nheaders:\n  Auth: Bearer ${PARITY_TOKEN}\nprojections:\n  tool1:\n    include_only: [x, y]\n  tool2:\n    exclude: [secret]\n",
				"servers/a.proj.yaml": "tool3:\n  include_only: [z]\n",
				"servers/b.yaml":      "command: echo\nprojections:\n  toolB:\n    include_only: [q]\n",
			},
			env: map[string]string{"PARITY_TOKEN": "tok123"},
		},
		{
			name:  "defined ${VAR} in headers expands while projection reference stays literal",
			files: map[string]string{"servers/svc.yaml": "url: https://x.example.com\nheaders:\n  Auth: Bearer ${PARITY_SVC_TOKEN}\nprojections:\n  t:\n    include_only: [\"${PARITY_SVC_FIELD}\"]\n"},
			env:   map[string]string{"PARITY_SVC_TOKEN": "tok", "PARITY_SVC_FIELD": "a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				testutil.WriteFile(t, filepath.Join(dir, rel), []byte(content))
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

func TestLoadLenientKeepsLoadableServersAndReportsBrokenSources(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "good.yaml"), []byte("command: echo\n"))
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), []byte("bad: [yaml\n"))
	testutil.WriteFile(t, filepath.Join(dir, "servers", "unset.yaml"), []byte("command: echo\nheaders:\n  X-Token: \"${LOAD_LENIENT_UNSET}\"\n"))
	servers, sourceErrors := config.LoadLenient(dir)
	var names []string
	for _, server := range servers {
		names = append(names, server.Name)
	}
	if slices.Sort(names); !slices.Equal(names, []string{"good", "unset"}) {
		t.Errorf("servers = %v, want [good unset]", names)
	}
	if len(sourceErrors) != 1 || sourceErrors[0].Path != filepath.Join(dir, "servers", "broken.yaml") {
		t.Errorf("source errors = %+v, want only broken.yaml", sourceErrors)
	}
}

func TestLoadLenientMergesKnownAuthWithoutOverridingServerAuth(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "detected.yaml"), []byte("command: echo\n"))
	testutil.WriteFile(t, filepath.Join(dir, "servers", "custom.yaml"), []byte("command: echo\nauth:\n  type: bearer\n"))
	if err := config.MarkOAuthDetected(dir, "detected"); err != nil {
		t.Fatal(err)
	}
	servers, _ := config.LoadLenient(dir)
	byName := map[string]config.ServerConfig{}
	for _, server := range servers {
		byName[server.Name] = server
	}
	if byName["detected"].Auth == nil || byName["detected"].Auth.Type != config.AuthTypeOAuth2 {
		t.Errorf("detected auth = %+v, want oauth2", byName["detected"].Auth)
	}
	if byName["custom"].Auth == nil || byName["custom"].Auth.Type != config.AuthTypeBearer {
		t.Errorf("custom auth = %+v, want bearer", byName["custom"].Auth)
	}
}

func TestLoadMainRefusesAConfigItCannotLoadInFull(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"valid settings load", "disable_auth_browser_open: true\n", ""},
		{"invalid YAML", "bad: [yaml\n", "parse config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), []byte(tt.yaml))
			cfg, err := config.LoadMain(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || cfg != nil {
					t.Errorf("LoadMain = (%#v, %v), want (nil, error containing %q) rather than defaults", cfg, err, tt.wantErr)
				}
				return
			}
			if err != nil || cfg == nil || !cfg.DisableAuthBrowserOpen {
				t.Errorf("LoadMain = (%#v, %v), want disable_auth_browser_open", cfg, err)
			}
		})
	}
}

func projKeys(m map[string]*config.ProjectionConfig) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestLoadProjections_projFilesSourceError(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "svc.yaml"), []byte("command: echo\n"))
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

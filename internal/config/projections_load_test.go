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
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLoadServerSet_brokenFileHoldsOnlyItsServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "good", Command: "echo"})
	if err := os.MkdirAll(filepath.Join(dir, "servers", "unreadable.yaml"), 0700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), "bad: [yaml\n")

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
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "linear",
		Transport: "http",
		URL:       "https://mcp.linear.app/mcp",
	})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "linear",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				IncludeOnly: []string{"title"},
			},
		},
	})
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := *config.FindServer(servers, "linear")
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), "bad: [yaml\n")

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
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "linear",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				Format: "bogus",
			},
		},
	})
	if _, err := config.LoadServer(dir, "linear"); err == nil {
		t.Error("LoadServer accepted a projection format Load rejects, so the server would stop mini's next start")
	}
}

func TestLoadServer_aNameDifferingOnlyInCaseIsNotFound(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "github",
		Transport: "http",
		URL:       "https://api.githubcopilot.com/mcp/",
	})

	if sc, err := config.LoadServer(dir, "GitHub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(GitHub) = %q, %v; want fs.ErrNotExist, not github.yaml under another name", sc.Name, err)
	}
}

func TestLoadProjections_parity(t *testing.T) {
	cases := []struct {
		name  string
		files projectionSources
		env   map[string]string
	}{
		{
			name: "multi-server with proj.yaml overlay",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "a",
						URL:     "https://a.example.com",
						Headers: map[string]string{"Auth": "Bearer ${PARITY_TOKEN}"},
						Projections: map[string]*config.ProjectionConfig{
							"tool1": {
								IncludeOnly: []string{"x", "y"},
							},
							"tool2": {
								Exclude: []string{"secret"},
							},
						},
					},
					{
						Name:    "b",
						Command: "echo",
						Projections: map[string]*config.ProjectionConfig{
							"toolB": {
								IncludeOnly: []string{"q"},
							},
						},
					},
				},
				Projections: []configtest.ProjectionFile{
					{
						ServerName: "a",
						Tools: map[string]*config.ProjectionConfig{
							"tool3": {
								IncludeOnly: []string{"z"},
							},
						},
					},
				},
			},
			env: map[string]string{"PARITY_TOKEN": "tok123"},
		},
		{
			name: "defined ${VAR} in headers expands while projection reference stays literal",
			files: projectionSources{
				Servers: []config.ServerConfig{
					{
						Name:    "svc",
						URL:     "https://x.example.com",
						Headers: map[string]string{"Auth": "Bearer ${PARITY_SVC_TOKEN}"},
						Projections: map[string]*config.ProjectionConfig{
							"t": {
								IncludeOnly: []string{"${PARITY_SVC_FIELD}"},
							},
						},
					},
				},
			},
			env: map[string]string{"PARITY_SVC_TOKEN": "tok", "PARITY_SVC_FIELD": "a"},
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
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "good", Command: "echo"})
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), "bad: [yaml\n")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "unset",
		Command: "echo",
		Headers: map[string]string{"X-Token": "${LOAD_LENIENT_UNSET}"},
	})
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
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "detected", Command: "echo"})
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "custom",
		Command: "echo",
		Auth: &config.AuthConfig{
			Type: "bearer",
		},
	})
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
	valid := config.DefaultConfig()
	valid.DisableAuthBrowserOpen = true
	badFormat := config.DefaultConfig()
	badFormat.ResponseFormat = "bogus"
	tests := []struct {
		name    string
		cfg     *config.Config
		rawYAML string
		wantErr string
	}{
		{"valid settings load", valid, "", ""},
		{"invalid YAML", nil, "bad: [yaml\n", "parse config"},
		{"invalid response_format", badFormat, "", "response_format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.cfg != nil {
				configtest.WriteConfig(t, dir, tt.cfg)
			} else {
				testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), tt.rawYAML)
			}
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
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "echo"})
	p := filepath.Join(dir, "servers", "svc.proj.yaml")
	if err := os.WriteFile(p, []byte("tool:\n  include_only: [a]\n"), 0000); err != nil { //fileiolint:allow unreadable fixture exercises load isolation
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

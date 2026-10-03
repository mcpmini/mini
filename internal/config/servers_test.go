//go:build test

package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

type loadServersCase struct {
	name       string
	files      map[string]string
	wantLoaded []string
	wantBroken []string
	check      func(*testing.T, config.Servers)
}

func TestLoadServers(t *testing.T) {
	cases := []loadServersCase{
		{
			name:  "orphan proj.yaml is ignored, even when broken",
			files: map[string]string{"servers/orphan.proj.yaml": "bad: [yaml\n"},
		},
		{
			name:       "undefined ${VAR} in a header loads the server with UnsetEnv",
			files:      map[string]string{"servers/svc.yaml": "url: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_TOKEN_XYZ}\nprojections:\n  t:\n    include_only: [a]\n"},
			wantLoaded: []string{"svc"},
			check: func(t *testing.T, servers config.Servers) {
				if sc, _ := servers.Find("svc"); sc.UnsetEnv == nil || sc.Projections["t"] == nil {
					t.Errorf("svc = %+v, want UnsetEnv set and its projections kept", sc)
				}
			},
		},
		{
			name:       "args ${VAR} breaks only that server",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nargs: [--token, \"${PROJ_TEST_TOK_XYZ}\"]\n", "servers/ok.yaml": "command: echo\n"},
			wantLoaded: []string{"ok"},
			wantBroken: []string{"svc"},
		},
		{
			name:       "undefined ${VAR} in a projection rule stays literal",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nprojections:\n  t:\n    include_only: [\"${PROJ_UNDEFINED_FIELD_XYZ}\"]\n"},
			wantLoaded: []string{"svc"},
			check: func(t *testing.T, servers config.Servers) {
				if sc, _ := servers.Find("svc"); sc.Projections["t"].IncludeOnly[0] != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("include_only = %q, want literal reference", sc.Projections["t"].IncludeOnly)
				}
			},
		},
		{
			name:       "bad projection format breaks that server",
			files:      map[string]string{"servers/github.yaml": "command: echo\nprojections:\n  t:\n    format: bad-format\n"},
			wantBroken: []string{"github"},
		},
		{
			name: "bad .proj.yaml breaks its server",
			files: map[string]string{
				"servers/b.yaml":      "command: echo\nprojections:\n  t:\n    include_only: [inline]\n",
				"servers/b.proj.yaml": "bad: [yaml\n",
			},
			wantBroken: []string{"b"},
		},
		{
			name: "the projection file overlays inline projections",
			files: map[string]string{
				"servers/a.yaml":      "command: echo\nprojections:\n  t1:\n    include_only: [inline]\n  t2:\n    include_only: [kept]\n",
				"servers/a.proj.yaml": "t1:\n  include_only: [file]\n",
			},
			wantLoaded: []string{"a"},
			check: func(t *testing.T, servers config.Servers) {
				sc, _ := servers.Find("a")
				if sc.Projections["t1"].IncludeOnly[0] != "file" || sc.Projections["t2"].IncludeOnly[0] != "kept" {
					t.Errorf("projections = t1 %v, t2 %v; want the file's t1 and the inline t2", sc.Projections["t1"].IncludeOnly, sc.Projections["t2"].IncludeOnly)
				}
			},
		},
		{
			name: "malformed servers/b.yaml breaks only b",
			files: map[string]string{
				"servers/a.yaml": "command: echo\n",
				"servers/b.yaml": "bad: [yaml\n",
			},
			wantLoaded: []string{"a"},
			wantBroken: []string{"b"},
		},
		{
			name: "invalid file name is broken under that name",
			files: map[string]string{
				"servers/good.yaml":     "command: echo\n",
				"servers/bad.name.yaml": "command: echo\n",
			},
			wantLoaded: []string{"good"},
			wantBroken: []string{"bad.name"},
		},
		{
			name: "config.yaml is not a server source, even when broken",
			files: map[string]string{
				"servers/file-svc.yaml": "command: echo\n",
				"config.yaml":           "bad: [yaml\n",
			},
			wantLoaded: []string{"file-svc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}
			checkLoadServers(t, config.LoadServers(dir), tc)
		})
	}
}

func checkLoadServers(t *testing.T, servers config.Servers, tc loadServersCase) {
	t.Helper()
	if got := loadedNames(servers); !slices.Equal(got, tc.wantLoaded) {
		t.Errorf("Loaded = %v, want %v", got, tc.wantLoaded)
	}
	if got := brokenNames(servers); !slices.Equal(got, tc.wantBroken) {
		t.Errorf("Broken = %v, want %v", got, tc.wantBroken)
	}
	for _, name := range tc.wantBroken {
		if !servers.IsBroken(name) {
			t.Errorf("IsBroken(%q) = false", name)
		}
	}
	if tc.check != nil {
		tc.check(t, servers)
	}
}

func loadedNames(servers config.Servers) []string {
	var names []string
	for _, sc := range servers.Loaded {
		names = append(names, sc.Name)
	}
	slices.Sort(names)
	return names
}

func brokenNames(servers config.Servers) []string {
	var names []string
	for _, b := range servers.Broken {
		names = append(names, b.ServerName)
	}
	slices.Sort(names)
	return names
}

func TestLoadServers_anUnreadableFileBreaksOnlyItsServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "good.yaml"), "command: echo\n")
	if err := os.MkdirAll(filepath.Join(dir, "servers", "unreadable.yaml"), 0700); err != nil {
		t.Fatal(err)
	}

	servers := config.LoadServers(dir)

	if !servers.IsEnabled("good") {
		t.Errorf("Loaded = %v, want good", loadedNames(servers))
	}
	if !servers.IsBroken("unreadable") {
		t.Error("IsBroken(unreadable) = false, want its running server kept")
	}
	for _, name := range []string{"good", "deleted"} {
		if servers.IsBroken(name) {
			t.Errorf("IsBroken(%q) = true, want only the failing file's server held", name)
		}
	}
}

func TestLoadServers_anUnreadableProjectionFileBreaksItsServer(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission test not meaningful")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), "command: echo\n")
	p := filepath.Join(dir, "servers", "svc.proj.yaml")
	if err := os.WriteFile(p, []byte("tool:\n  include_only: [a]\n"), 0000); err != nil {
		t.Fatal(err)
	}

	servers := config.LoadServers(dir)

	if !servers.IsBroken("svc") || len(servers.Loaded) != 0 {
		t.Errorf("Loaded = %v, Broken = %v; want svc broken, not loaded without its projections", loadedNames(servers), brokenNames(servers))
	}
}

func TestLoadServers_mergesKnownAuthWithoutOverridingServerAuth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "detected.yaml"), "command: echo\n")
	writeFile(t, filepath.Join(dir, "servers", "custom.yaml"), "command: echo\nauth:\n  type: bearer\n")
	if err := config.MarkOAuthDetected(dir, "detected"); err != nil {
		t.Fatal(err)
	}

	servers := config.LoadServers(dir)

	if sc, _ := servers.Find("detected"); sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 {
		t.Errorf("detected auth = %+v, want oauth2", sc.Auth)
	}
	if sc, _ := servers.Find("custom"); sc.Auth == nil || sc.Auth.Type != config.AuthTypeBearer {
		t.Errorf("custom auth = %+v, want bearer", sc.Auth)
	}
}

func TestLoadServer_matchesLoadServersWithoutNeedingTheOtherFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "linear.yaml"), "transport: http\nurl: https://mcp.linear.app/mcp\n")
	writeFile(t, filepath.Join(dir, "servers", "linear.proj.yaml"), "list_issues:\n  include_only: [title]\n")
	want, _ := config.LoadServers(dir).Find("linear")
	writeFile(t, filepath.Join(dir, "servers", "broken.yaml"), "bad: [yaml\n")

	got, err := config.LoadServer(dir, "linear")

	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadServer = %+v\nwant what LoadServers gives: %+v", got, want)
	}
	if got.Auth == nil || got.Projections["list_issues"] == nil {
		t.Errorf("LoadServer = %+v, want bundled auth and the projection file merged", got)
	}
	if _, err := config.LoadServer(dir, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(missing) err = %v, want fs.ErrNotExist", err)
	}
	writeFile(t, filepath.Join(dir, "servers", "linear.proj.yaml"), "list_issues:\n  format: bogus\n")
	if _, err := config.LoadServer(dir, "linear"); err == nil {
		t.Error("LoadServer accepted a projection format LoadServers rejects")
	}
}

func TestLoadServer_aNameDifferingOnlyInCaseIsNotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "servers", "github.yaml"), "transport: http\nurl: https://api.githubcopilot.com/mcp/\n")

	if sc, err := config.LoadServer(dir, "GitHub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(GitHub) = %q, %v; want fs.ErrNotExist, not github.yaml under another name", sc.Name, err)
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
		{"invalid response_format", "response_format: bogus\n", "response_format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "config.yaml"), tt.yaml)
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

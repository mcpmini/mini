//go:build test

package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
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
			name:       "leftover projection filename is a broken source while its sibling loads",
			files:      map[string]string{"servers/orphan.proj.yaml": "bad: [yaml\n", "servers/good.yaml": "command: echo\n"},
			wantLoaded: []string{"good"},
			wantBroken: []string{"orphan.proj"},
		},
		{
			name:       "undefined environment reference in header loads with UnsetEnv",
			files:      map[string]string{"servers/svc.yaml": "url: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_TOKEN_XYZ}\nprojections:\n  t: {include_only: [a]}\n"},
			wantLoaded: []string{"svc"},
			check: func(t *testing.T, servers config.Servers) {
				if sc, _ := servers.Find("svc"); sc.UnsetEnv == nil || sc.Projections["t"] == nil {
					t.Errorf("svc = %+v, want UnsetEnv and projections", sc)
				}
			},
		},
		{
			name:       "undefined environment reference in args breaks only that server",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nargs: [--token, \"${PROJ_TEST_TOK_XYZ}\"]\n", "servers/ok.yaml": "command: echo\n"},
			wantLoaded: []string{"ok"},
			wantBroken: []string{"svc"},
		},
		{
			name:       "undefined environment references in projections stay literal",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nprojections:\n  t:\n    include_only: [\"${PROJ_UNDEFINED_FIELD_XYZ}\"]\n"},
			wantLoaded: []string{"svc"},
			check: func(t *testing.T, servers config.Servers) {
				if sc, _ := servers.Find("svc"); sc.Projections["t"].IncludeOnly[0] != "${PROJ_UNDEFINED_FIELD_XYZ}" {
					t.Errorf("include_only = %q, want literal reference", sc.Projections["t"].IncludeOnly)
				}
			},
		},
		{
			name:       "bad inline projection format keeps server loaded without projections",
			files:      map[string]string{"servers/github.yaml": "command: echo\nprojections:\n  t:\n    format: bad-format\n"},
			wantLoaded: []string{"github"},
			check:      wantUnprojected("github", "github.yaml"),
		},
		{
			name:       "invalid inline projection structure keeps server loaded without projections",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nprojections:\n  t:\n    include_only: 5\n"},
			wantLoaded: []string{"svc"},
			check:      wantUnprojected("svc", "svc.yaml"),
		},
		{
			name:       "inline projections reached through a merge key apply",
			files:      map[string]string{"servers/merged.yaml": "base: &base\n  projections:\n    t:\n      include_only: [merged]\n<<: *base\ncommand: echo\n"},
			wantLoaded: []string{"merged"},
			check: func(t *testing.T, servers config.Servers) {
				if sc, _ := servers.Find("merged"); sc.Projections["t"].IncludeOnly[0] != "merged" {
					t.Errorf("merged projections = %#v", sc.Projections)
				}
			},
		},
		{
			name:       "malformed inherited projection block remains a projection error",
			files:      map[string]string{"servers/svc.yaml": "base: &base\n  projections: {t: {include_only: 5}}\n<<: *base\ncommand: echo\n"},
			wantLoaded: []string{"svc"},
			check:      wantUnprojected("svc", "svc.yaml"),
		},
		{
			name:       "direct projections replace inherited projections during load",
			files:      map[string]string{"servers/svc.yaml": "base: &base\n  projections: {t: {include_only: [merged]}, u: {include_only: [merged]}}\nprojections: {t: {include_only: [own]}}\n<<: *base\ncommand: echo\n"},
			wantLoaded: []string{"svc"},
			check: func(t *testing.T, servers config.Servers) {
				sc, _ := servers.Find("svc")
				if !reflect.DeepEqual(projectionRules(sc), map[string][]string{"t": {"own"}}) {
					t.Errorf("projections = %v", projectionRules(sc))
				}
			},
		},
		{
			name:       "duplicate projections keys break the server",
			files:      map[string]string{"servers/svc.yaml": "command: echo\nprojections: {}\nprojections: {}\n"},
			wantBroken: []string{"svc"},
		},
		{
			name:       "malformed server breaks only itself",
			files:      map[string]string{"servers/a.yaml": "command: echo\n", "servers/b.yaml": "bad: [yaml\n"},
			wantLoaded: []string{"a"},
			wantBroken: []string{"b"},
		},
		{
			name:       "invalid file name is broken under that name",
			files:      map[string]string{"servers/good.yaml": "command: echo\n", "servers/bad.name.yaml": "command: echo\n"},
			wantLoaded: []string{"good"},
			wantBroken: []string{"bad.name"},
		},
		{
			name:       "config.yaml is not a server source",
			files:      map[string]string{"servers/file-svc.yaml": "command: echo\n", "config.yaml": "bad: [yaml\n"},
			wantLoaded: []string{"file-svc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				testutil.WriteFile(t, filepath.Join(dir, rel), content)
			}
			checkLoadServers(t, mustLoadServers(t, dir), tc)
		})
	}
}

func wantUnprojected(name, file string) func(*testing.T, config.Servers) {
	return func(t *testing.T, servers config.Servers) {
		t.Helper()
		sc, _ := servers.Find(name)
		if sc.ProjectionsErr == nil || filepath.Base(sc.ProjectionsErr.Path) != file || sc.Projections != nil {
			t.Errorf("%s = projections %v, error %+v; want no projections and an error blaming %s", name, sc.Projections, sc.ProjectionsErr, file)
		}
		if broken := servers.BrokenProjections(); len(broken) != 1 || broken[0].ServerName != name {
			t.Errorf("BrokenProjections = %+v, want only %s", broken, name)
		}
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

func TestLoadServers_aConfigPathWithGlobSyntaxStillListsItsServers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd[name")
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "echo"})

	if got := loadedNames(mustLoadServers(t, dir)); !slices.Equal(got, []string{"svc"}) {
		t.Errorf("Loaded = %v, want [svc]", got)
	}
}

func TestLoadServers_failsWhenItCannotListTheServerFiles(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers"), "not a directory\n")

	if _, err := config.LoadServers(dir); err == nil {
		t.Error("LoadServers = nil error, want one: an empty result would read as every server removed")
	}
}

func TestLoadServers_anUnreadableFileBreaksOnlyItsServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "good", Command: "echo"})
	if err := os.MkdirAll(filepath.Join(dir, "servers", "unreadable.yaml"), 0700); err != nil {
		t.Fatal(err)
	}

	servers := mustLoadServers(t, dir)

	if !servers.IsEnabled("good") {
		t.Errorf("Loaded = %v, want good", loadedNames(servers))
	}
	if !servers.IsBroken("unreadable") || servers.IsBroken("good") {
		t.Errorf("Broken = %+v, want only unreadable", servers.Broken)
	}
}

func TestLoadServers_anUnreadableLegacyProjectionFileIsBrokenWithoutAffectingItsServer(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{"tool": {Exclude: []string{"secret"}}}})
	if err := os.MkdirAll(filepath.Join(dir, "servers", "svc.proj.yaml"), 0700); err != nil {
		t.Fatal(err)
	}
	servers := mustLoadServers(t, dir)
	if !servers.IsEnabled("svc") || servers.IsBroken("svc") {
		t.Fatalf("server state = %+v, want valid sibling loaded", servers)
	}
	if !servers.IsBroken("svc.proj") {
		t.Fatalf("broken sources = %+v, want the legacy file reported", servers.Broken)
	}
	if sc, _ := servers.Find("svc"); sc.Projections["tool"] == nil || sc.Projections["tool"].Exclude[0] != "secret" {
		t.Fatalf("valid sibling projections = %#v, want secret exclusion preserved", sc.Projections)
	}
}

func TestLoadServers_mergesKnownAuthWithoutOverridingServerAuth(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "detected", Command: "echo"})
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "custom", Command: "echo", Auth: &config.AuthConfig{Type: config.AuthTypeBearer}})
	if err := config.MarkOAuthDetected(dir, "detected"); err != nil {
		t.Fatal(err)
	}

	servers := mustLoadServers(t, dir)

	if sc, _ := servers.Find("detected"); sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 {
		t.Errorf("detected auth = %+v, want oauth2", sc.Auth)
	}
	if sc, _ := servers.Find("custom"); sc.Auth == nil || sc.Auth.Type != config.AuthTypeBearer {
		t.Errorf("custom auth = %+v, want bearer", sc.Auth)
	}
}

func TestLoadServer_matchesLoadServersWithoutNeedingTheOtherFiles(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "linear", Transport: "http", URL: "https://mcp.linear.app/mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "linear",
		Tools:      map[string]*config.ProjectionConfig{"list_issues": {IncludeOnly: []string{"title"}}},
	})
	want, _ := mustLoadServers(t, dir).Find("linear")
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), "bad: [yaml\n")

	got, err := config.LoadServer(dir, "linear")

	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadServer = %+v\nwant what LoadServers gives: %+v", got, want)
	}
	if got.Auth == nil || got.Projections["list_issues"] == nil {
		t.Errorf("LoadServer = %+v, want bundled auth and inline projections", got)
	}
	if _, err := config.LoadServer(dir, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(missing) err = %v, want fs.ErrNotExist", err)
	}
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "linear",
		Tools:      map[string]*config.ProjectionConfig{"list_issues": {Format: "bogus"}},
	})
	if got, _ := config.LoadServer(dir, "linear"); got.ProjectionsErr == nil {
		t.Error("LoadServer accepted a projection format LoadServers rejects")
	}
}

func TestLoadServer_aNameDifferingOnlyInCaseIsNotFound(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "github", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"})

	if sc, err := config.LoadServer(dir, "GitHub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadServer(GitHub) = %q, %v; want fs.ErrNotExist, not github.yaml under another name", sc.Name, err)
	}
}

func TestLoadServer_readsBackTheProjectionsAServerFileWasWrittenWith(t *testing.T) {
	dir := t.TempDir()
	written := config.ServerConfig{Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{
		"t": {IncludeOnly: []string{"id"}, ArrayLimits: map[string]int{"items": 5}},
	}}
	configtest.WriteServer(t, dir, written)

	got, err := config.LoadServer(dir, "svc")

	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if !reflect.DeepEqual(got.Projections, written.Projections) || got.ProjectionsErr != nil {
		t.Errorf("projections = %+v, error %+v; want %+v as written", got.Projections["t"], got.ProjectionsErr, written.Projections["t"])
	}
}

func projectionRules(sc config.ServerConfig) map[string][]string {
	rules := map[string][]string{}
	for tool, p := range sc.Projections {
		rules[tool] = p.IncludeOnly
	}
	return rules
}

package ops_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/testutil"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

func TestAddServer_writtenFile(t *testing.T) {
	t.Run("roundtrips command and args", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"-y", "server-github"}}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		var got config.ServerConfig
		readYAML(t, filepath.Join(dir, "servers", "gh.yaml"), &got)
		if got.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", got.Command)
		}
		if len(got.Args) != 2 || got.Args[0] != "-y" || got.Args[1] != "server-github" {
			t.Errorf("Args = %v, want [-y server-github]", got.Args)
		}
	})

	t.Run("roundtrips url and transport", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "remote", Transport: "http", URL: "https://example.com/mcp"}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		var got config.ServerConfig
		readYAML(t, filepath.Join(dir, "servers", "remote.yaml"), &got)
		if got.Transport != "http" {
			t.Errorf("Transport = %q, want 'http'", got.Transport)
		}
		if got.URL != "https://example.com/mcp" {
			t.Errorf("URL = %q, want 'https://example.com/mcp'", got.URL)
		}
	})

	t.Run("roundtrips permissions", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{
			Name:    "guarded",
			Command: "run",
			Permissions: &config.PermissionsConfig{
				Protected: []string{"dangerous_tool"},
				Hidden:    []string{"internal_tool"},
			},
		}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		var got config.ServerConfig
		readYAML(t, filepath.Join(dir, "servers", "guarded.yaml"), &got)
		if got.Permissions == nil {
			t.Fatal("Permissions is nil after roundtrip")
		}
		if len(got.Permissions.Protected) != 1 || got.Permissions.Protected[0] != "dangerous_tool" {
			t.Errorf("Protected = %v, want [dangerous_tool]", got.Permissions.Protected)
		}
	})

	t.Run("empty stdio fields absent from yaml for http server", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "http-only", Transport: "http", URL: "https://example.com"}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		data := testutil.ReadFile(t, filepath.Join(dir, "servers", "http-only.yaml"))
		for _, unwanted := range []string{"command:", "args:", "env:"} {
			if strings.Contains(string(data), unwanted) {
				t.Errorf("yaml contains %q for empty field", unwanted)
			}
		}
	})

	t.Run("file has 0600 permissions", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "sec", Command: "run"}); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		info, _ := os.Stat(filepath.Join(dir, "servers", "sec.yaml"))
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("perm = %04o, want 0600", perm)
		}
	})

	t.Run("known server installs bundled projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		got, err := config.LoadServer(dir, "gh")
		if err != nil || len(got.Projections) == 0 {
			t.Errorf("LoadServer projections = %#v, %v; want bundled defaults inline", got.Projections, err)
		}
	})

	t.Run("known server installs bundled permissions when none specified", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		var got config.ServerConfig
		readYAML(t, filepath.Join(dir, "servers", "gh.yaml"), &got)
		if got.Permissions == nil {
			t.Fatal("expected bundled permissions to be applied")
		}
		if len(got.Permissions.Hidden) == 0 {
			t.Error("expected hidden tools in bundled github permissions")
		}
	})

	t.Run("explicit permissions take precedence over bundled", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{
			Name:      "gh",
			Transport: "http",
			URL:       "https://api.github.com/mcp",
			Permissions: &config.PermissionsConfig{
				Protected: []string{"my_tool"},
			},
		}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		var got config.ServerConfig
		readYAML(t, filepath.Join(dir, "servers", "gh.yaml"), &got)
		if got.Permissions == nil || len(got.Permissions.Protected) != 1 {
			t.Fatalf("expected explicit permissions preserved, got %+v", got.Permissions)
		}
		if got.Permissions.Protected[0] != "my_tool" {
			t.Errorf("Protected = %v, want [my_tool]", got.Permissions.Protected)
		}
		if len(got.Permissions.Hidden) != 0 {
			t.Errorf("bundled hidden applied despite explicit permissions: %v", got.Permissions.Hidden)
		}
	})
}

func TestAddServer(t *testing.T) {
	t.Run("writes a new server that loads back under its name", func(t *testing.T) {
		dir := tempDir(t)
		github := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}

		added, err := ops.AddServer(dir, github)

		if err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		if added.Path != filepath.Join(dir, "servers", "gh.yaml") || !added.DefaultProjections {
			t.Errorf("added = %+v, want inline defaults in %s", added, filepath.Join(dir, "servers", "gh.yaml"))
		}
		if !added.DefaultPermissions || added.Config.Permissions == nil {
			t.Errorf("added = %+v, want github's bundled permissions applied and reported", added)
		}
		if got, err := config.LoadServer(dir, "gh"); err != nil || got.URL != github.URL {
			t.Errorf("LoadServer(gh) = %#v, %v; want gh with URL %q", got, err, github.URL)
		}
	})

	t.Run("a reused name never inherits the old server's token, registration, OAuth marker or projections", func(t *testing.T) {
		dir := tempDir(t)
		saveCredentials(t, dir, "reused")
		if err := config.MarkOAuthDetected(dir, "reused"); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(dir, "servers", "reused.proj.yaml")
		testutil.WriteFile(t, legacy, "list: {include_only: [id]}\n")

		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "reused", Command: "run"}); err != nil {
			t.Fatal(err)
		}

		assertNoCredentials(t, dir, "reused")
		if config.IsOAuthDetected(dir, "reused") {
			t.Error("the new server inherited the old server's OAuth marker")
		}
		if got, err := config.LoadServer(dir, "reused"); err != nil || len(got.Projections) != 0 {
			t.Errorf("new server projections = %#v, %v; want no legacy rules", got.Projections, err)
		}
	})

	t.Run("a reused vendor name gets the bundled projection, not the old server's", func(t *testing.T) {
		dir := tempDir(t)
		leftover := filepath.Join(dir, "servers", "gh.proj.yaml")
		testutil.WriteFile(t, leftover, "# the old server's rules\n")

		added, err := ops.AddServer(dir, config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"})
		if err != nil {
			t.Fatal(err)
		}

		got := testutil.ReadFile(t, leftover)
		loaded, loadErr := config.LoadServer(dir, "gh")
		if !added.DefaultProjections || loadErr != nil || len(loaded.Projections) == 0 {
			t.Errorf("added defaults=%v, inline projections=%#v, err=%v", added.DefaultProjections, loaded.Projections, loadErr)
		}
		if !strings.Contains(string(got), "the old server's rules") {
			t.Errorf("legacy file changed: %q", got)
		}
	})

	t.Run("a failure to forget old state leaves nothing written", func(t *testing.T) {
		dir := tempDir(t)
		testutil.WriteFile(t, filepath.Join(dir, "internal", "stuck.token.json", "pinned"), "")

		_, err := ops.AddServer(dir, config.ServerConfig{Name: "stuck", Command: "run"})

		if err == nil || !strings.Contains(err.Error(), "forget stuck credentials") {
			t.Fatalf("err = %v, want the credential cleanup error", err)
		}
		if fileExists(filepath.Join(dir, "servers", "stuck.yaml")) {
			t.Error("the server file stayed while its name may still hand out the old token")
		}
	})

	t.Run("prints nothing, since the config tool calls it where stdout is the MCP stream", func(t *testing.T) {
		dir := tempDir(t)
		printed := testutil.CaptureStdout(t, func() {
			if _, err := ops.AddServer(dir, config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}); err != nil {
				t.Fatal(err)
			}
		})
		if printed != "" {
			t.Errorf("AddServer printed %q", printed)
		}
	})

	for name, existing := range map[string]string{
		"a configured server":                          "command: original\n",
		"a file that fails to load, possibly mid-edit": "command: [unfinished\n",
	} {
		t.Run("refuses "+name+" and keeps its state", func(t *testing.T) {
			dir := tempDir(t)
			path := filepath.Join(dir, "servers", "taken.yaml")
			testutil.WriteFile(t, path, existing)
			saveCredentials(t, dir, "taken")

			_, err := ops.AddServer(dir, config.ServerConfig{Name: "taken", Command: "replacement"})

			if !errors.Is(err, ops.ErrAlreadyConfigured) {
				t.Fatalf("err = %v, want ErrAlreadyConfigured", err)
			}
			if got := testutil.ReadFile(t, path); string(got) != existing {
				t.Errorf("file = %q, want it untouched", got)
			}
			if _, err := auth.Load(dir, "taken"); err != nil {
				t.Errorf("the configured server lost its token: %v", err)
			}
		})
	}

	t.Run("a config that would not load is refused with nothing written", func(t *testing.T) {
		dir := tempDir(t)
		_, err := ops.AddServer(dir, config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp?t=${GITHUB_TOKEN}"})
		if err == nil || !strings.Contains(err.Error(), "isn't expanded") {
			t.Fatalf("err = %v, want the load error", err)
		}
		if fileExists(filepath.Join(dir, "servers", "gh.yaml")) {
			t.Error("a server file was written")
		}
		if got, err := config.LoadServer(dir, "gh"); err == nil {
			t.Errorf("a refused server was written: %+v", got)
		}
	})

	t.Run("concurrent adds of one name: exactly one wins", func(t *testing.T) {
		dir := tempDir(t)
		errs := make(chan error, 8)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				_, err := ops.AddServer(dir, config.ServerConfig{Name: "race", Command: fmt.Sprintf("run-%d", i)})
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		wins := 0
		for err := range errs {
			switch {
			case err == nil:
				wins++
			case !errors.Is(err, ops.ErrAlreadyConfigured):
				t.Errorf("err = %v, want nil or ErrAlreadyConfigured", err)
			}
		}
		if wins != 1 {
			t.Errorf("%d adds succeeded, want 1", wins)
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		if _, err := ops.AddServer(tempDir(t), config.ServerConfig{Name: "bad name!"}); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

func TestRemoveServer(t *testing.T) {
	t.Run("removes the server file and forgets the token, registration and OAuth marker", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "toremove", Command: "run"}); err != nil {
			t.Fatal(err)
		}
		configtest.WriteProjections(t, dir, configtest.ProjectionFile{
			ServerName: "toremove",
			Tools: map[string]*config.ProjectionConfig{
				"list": {
					IncludeOnly: []string{"id"},
				},
			},
		})
		saveCredentials(t, dir, "toremove")
		if err := config.MarkOAuthDetected(dir, "toremove"); err != nil {
			t.Fatal(err)
		}

		if err := ops.RemoveServer(dir, "toremove"); err != nil {
			t.Fatalf("RemoveServer: %v", err)
		}

		if fileExists(filepath.Join(dir, "servers", "toremove.yaml")) {
			t.Error("server file still exists after remove")
		}
		assertNoCredentials(t, dir, "toremove")
		if config.IsOAuthDetected(dir, "toremove") {
			t.Error("a server reusing this name would inherit a stale OAuth marker")
		}
	})

	t.Run("a server added again after a remove gets the bundled projection again", func(t *testing.T) {
		dir := tempDir(t)
		github := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"}
		if _, err := ops.AddServer(dir, github); err != nil {
			t.Fatal(err)
		}
		if err := ops.RemoveServer(dir, "gh"); err != nil {
			t.Fatal(err)
		}

		added, err := ops.AddServer(dir, github)
		if err != nil {
			t.Fatal(err)
		}
		if !added.DefaultProjections || len(added.Config.Projections) == 0 {
			t.Error("the re-added server has no inline bundled projections")
		}
	})

	t.Run("a failed cleanup keeps the server so the remove can be retried", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "stuck", Command: "run"}); err != nil {
			t.Fatal(err)
		}
		pinned := filepath.Join(dir, "internal", "stuck.token.json", "pinned")
		testutil.WriteFile(t, pinned, "")

		if err := ops.RemoveServer(dir, "stuck"); err == nil {
			t.Fatal("RemoveServer succeeded while the token could not be deleted")
		}
		if !fileExists(filepath.Join(dir, "servers", "stuck.yaml")) {
			t.Fatal("the server file went before its credentials, so a retry cannot finish the cleanup")
		}

		if err := os.Remove(pinned); err != nil {
			t.Fatal(err)
		}
		if err := ops.RemoveServer(dir, "stuck"); err != nil {
			t.Fatalf("retried RemoveServer: %v", err)
		}
		assertNoCredentials(t, dir, "stuck")
	})

	t.Run("a name differing only in case removes nothing", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "github", Command: "run"}); err != nil {
			t.Fatal(err)
		}
		saveCredentials(t, dir, "github")

		if err := ops.RemoveServer(dir, "GitHub"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("RemoveServer(GitHub) = %v, want fs.ErrNotExist", err)
		}
		if !fileExists(filepath.Join(dir, "servers", "github.yaml")) {
			t.Error("removing GitHub deleted github.yaml")
		}
	})

	t.Run("returns ErrNotExist for a server that isn't configured", func(t *testing.T) {
		if err := ops.RemoveServer(tempDir(t), "ghost"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("returns error for invalid name", func(t *testing.T) {
		if err := ops.RemoveServer(tempDir(t), "bad name!"); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func saveCredentials(t *testing.T, dir, name string) {
	t.Helper()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: name,
		Token: &oauth2.Token{
			AccessToken: "old-server-token",
		},
	})
	authtest.SaveRegistration(t, authtest.RegistrationFile{
		ConfigDir:  dir,
		ServerName: name,
		Registration: &auth.Registration{
			ClientID: "old-client",
		},
	})
}

func assertNoCredentials(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := auth.Load(dir, name); !auth.IsNotFound(err) {
		t.Errorf("token for %s still stored (err = %v); a server reusing the name would be sent it", name, err)
	}
	if _, err := auth.LoadRegistration(dir, name); !auth.IsNotFound(err) {
		t.Errorf("client registration for %s still stored (err = %v)", name, err)
	}
}

func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	data := testutil.ReadFile(t, path)
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

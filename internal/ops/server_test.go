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

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestWriteServer(t *testing.T) {
	t.Run("roundtrips command and args", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"-y", "server-github"}}
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
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
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
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
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
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
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "servers", "http-only.yaml"))
		for _, unwanted := range []string{"command:", "args:", "env:"} {
			if strings.Contains(string(data), unwanted) {
				t.Errorf("yaml contains %q for empty field", unwanted)
			}
		}
	})

	t.Run("file has 0600 permissions", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.WriteServer(dir, config.ServerConfig{Name: "sec", Command: "run"}); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		info, _ := os.Stat(filepath.Join(dir, "servers", "sec.yaml"))
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("perm = %04o, want 0600", perm)
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.WriteServer(dir, config.ServerConfig{Name: "bad name!"}); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})

	t.Run("known server installs bundled projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		dest := filepath.Join(dir, "servers", "gh.proj.yaml")
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("bundled projection not installed: %v", err)
		}
		if len(data) == 0 {
			t.Error("bundled projection file is empty")
		}
	})

	t.Run("known server installs bundled permissions when none specified", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
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
		if _, err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
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
		if added.Path != filepath.Join(dir, "servers", "gh.yaml") || added.ProjectionPath != filepath.Join(dir, "servers", "gh.proj.yaml") {
			t.Errorf("paths = %q, %q", added.Path, added.ProjectionPath)
		}
		if !added.DefaultPermissions || added.Config.Permissions == nil {
			t.Errorf("added = %+v, want github's bundled permissions applied and reported", added)
		}
		_, servers, err := config.Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := config.FindServer(servers, "gh"); got == nil || got.URL != github.URL {
			t.Errorf("loaded servers = %#v, want gh with URL %q", servers, github.URL)
		}
	})

	t.Run("a reused name never inherits the old server's token, registration, OAuth marker or projections", func(t *testing.T) {
		dir := tempDir(t)
		saveCredentials(t, dir, "reused")
		if err := config.MarkOAuthDetected(dir, "reused"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "servers", "reused.proj.yaml"), "list:\n  include_only: [id]\n")

		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "reused", Command: "run"}); err != nil {
			t.Fatal(err)
		}

		assertNoCredentials(t, dir, "reused")
		if config.IsOAuthDetected(dir, "reused") {
			t.Error("the new server inherited the old server's OAuth marker")
		}
		if fileExists(filepath.Join(dir, "servers", "reused.proj.yaml")) {
			t.Error("the new server inherited the old server's projections")
		}
	})

	t.Run("a reused vendor name gets the bundled projection, not the old server's", func(t *testing.T) {
		dir := tempDir(t)
		leftover := filepath.Join(dir, "servers", "gh.proj.yaml")
		writeFile(t, leftover, "# the old server's rules\n")

		added, err := ops.AddServer(dir, config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"})
		if err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(leftover)
		if err != nil {
			t.Fatal(err)
		}
		if added.ProjectionPath != leftover || len(got) == 0 || strings.Contains(string(got), "the old server's rules") {
			t.Errorf("projection path %q holds %q, want the bundled projection installed in place of the leftover", added.ProjectionPath, got)
		}
	})

	t.Run("a failure to forget old state leaves nothing written", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "internal", "stuck.token.json", "pinned"), "")

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
			writeFile(t, path, existing)
			saveCredentials(t, dir, "taken")

			_, err := ops.AddServer(dir, config.ServerConfig{Name: "taken", Command: "replacement"})

			if !errors.Is(err, ops.ErrAlreadyConfigured) {
				t.Fatalf("err = %v, want ErrAlreadyConfigured", err)
			}
			if got, _ := os.ReadFile(path); string(got) != existing {
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
		if _, err := os.Stat(filepath.Join(dir, "servers", "gh.proj.yaml")); err == nil {
			t.Error("a bundled projection was installed for a refused server")
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
	t.Run("removes the file and its projections, and forgets the token, registration and OAuth marker", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "toremove", Command: "run"}); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "servers", "toremove.proj.yaml"), "list:\n  include_only: [id]\n")
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
		if fileExists(filepath.Join(dir, "servers", "toremove.proj.yaml")) {
			t.Error("a server reusing this name would inherit the removed server's projections")
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
		if added.ProjectionPath == "" {
			t.Error("the re-added server kept no bundled projection; the removed server's file was left in the way")
		}
	})

	t.Run("a failed cleanup keeps the server so the remove can be retried", func(t *testing.T) {
		dir := tempDir(t)
		if _, err := ops.AddServer(dir, config.ServerConfig{Name: "stuck", Command: "run"}); err != nil {
			t.Fatal(err)
		}
		pinned := filepath.Join(dir, "internal", "stuck.token.json", "pinned")
		writeFile(t, pinned, "")

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
	if err := auth.Save(dir, name, &oauth2.Token{AccessToken: "old-server-token"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveRegistration(dir, name, &auth.Registration{ClientID: "old-client"}); err != nil {
		t.Fatal(err)
	}
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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

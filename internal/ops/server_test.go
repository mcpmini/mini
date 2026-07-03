package ops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestWriteServer(t *testing.T) {
	t.Run("roundtrips command and args", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"-y", "server-github"}}
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, config.ServerConfig{Name: "sec", Command: "run"}); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		info, _ := os.Stat(filepath.Join(dir, "servers", "sec.yaml"))
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("perm = %04o, want 0600", perm)
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		if err := ops.WriteServer(dir, config.ServerConfig{Name: "bad name!"}); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})

	t.Run("known server installs bundled projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.github.com/mcp"}
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, sc); err != nil {
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
		if err := ops.WriteServer(dir, sc); err != nil {
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

func TestDeleteServer(t *testing.T) {
	t.Run("removes the server file", func(t *testing.T) {
		dir := tempDir(t)
		ops.WriteServer(dir, config.ServerConfig{Name: "toremove", Command: "run"}) //nolint:errcheck
		if err := ops.DeleteServer(dir, "toremove"); err != nil {
			t.Fatalf("DeleteServer: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "toremove.yaml")); err == nil {
			t.Fatal("server file still exists after delete")
		}
	})

	t.Run("returns error for non-existent server", func(t *testing.T) {
		dir := tempDir(t)
		if err := ops.DeleteServer(dir, "ghost"); err == nil {
			t.Fatal("expected error deleting non-existent server")
		}
	})

	t.Run("returns error for invalid name", func(t *testing.T) {
		dir := tempDir(t)
		if err := ops.DeleteServer(dir, "bad name!"); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})

	t.Run("also clears the oauth-detected marker", func(t *testing.T) {
		dir := tempDir(t)
		ops.WriteServer(dir, config.ServerConfig{Name: "toremove", Command: "run"}) //nolint:errcheck
		if err := config.MarkOAuthDetected(dir, "toremove"); err != nil {
			t.Fatalf("MarkOAuthDetected: %v", err)
		}
		if err := ops.DeleteServer(dir, "toremove"); err != nil {
			t.Fatalf("DeleteServer: %v", err)
		}
		if config.IsOAuthDetected(dir, "toremove") {
			t.Error("a server reusing this name would inherit a stale oauth-detected marker")
		}
	})
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

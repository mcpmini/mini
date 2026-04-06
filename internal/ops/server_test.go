//go:build test

package ops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestWriteServer(t *testing.T) {
	t.Run("valid server creates yaml", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"server-github"}}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "gh.yaml")); err != nil {
			t.Fatalf("server file not created: %v", err)
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

	t.Run("known server installs projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", URL: "https://api.github.com/mcp", Transport: "http"}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "projections", "gh.yaml")); err != nil {
			t.Fatalf("bundled projection not installed: %v", err)
		}
	})

	t.Run("omitempty fields absent from yaml for http server", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "http-only", URL: "https://example.com", Transport: "http"}
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
}

func TestDeleteServer(t *testing.T) {
	t.Run("deletes existing server", func(t *testing.T) {
		dir := tempDir(t)
		ops.WriteServer(dir, config.ServerConfig{Name: "toremove", Command: "run"}) //nolint:errcheck
		if err := ops.DeleteServer(dir, "toremove"); err != nil {
			t.Fatalf("DeleteServer: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "toremove.yaml")); err == nil {
			t.Fatal("server file still exists after delete")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		dir := tempDir(t)
		if err := ops.DeleteServer(dir, "ghost"); err == nil {
			t.Fatal("expected error deleting non-existent server")
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		if err := ops.DeleteServer(dir, "bad name!"); err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

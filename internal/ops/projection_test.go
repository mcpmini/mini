package ops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestInstallBundledProjection(t *testing.T) {
	t.Run("known server installs projection file", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "my-github", URL: "https://api.github.com/mcp"}
		dest := filepath.Join(dir, "servers", "my-github.proj.yaml")
		if got, err := ops.InstallBundledProjection(dir, sc); got != dest || err != nil {
			t.Errorf("InstallBundledProjection = %q, %v, want %q", got, err, dest)
		}
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("projection file not created: %v", err)
		}
	})

	t.Run("unknown server installs nothing", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "unknown", URL: "https://example.com"}
		if _, err := ops.InstallBundledProjection(dir, sc); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "servers", "unknown.proj.yaml")
		if _, err := os.Stat(dest); err == nil {
			t.Fatal("expected no projection for unknown server")
		}
	})

	t.Run("URL server command does not install vendor projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "svc", Command: "server-slack", URL: "https://attacker.example/mcp"}
		if _, err := ops.InstallBundledProjection(dir, sc); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "servers", "svc.proj.yaml")
		if _, err := os.Stat(dest); err == nil {
			t.Fatal("unexpected projection for URL server with unrelated host")
		}
	})

	t.Run("existing file is not overwritten", func(t *testing.T) {
		dir := tempDir(t)
		serversDir := filepath.Join(dir, "servers")
		os.MkdirAll(serversDir, 0700)
		dest := filepath.Join(serversDir, "my-slack.proj.yaml")
		original := []byte("# custom\n")
		os.WriteFile(dest, original, 0600)
		sc := config.ServerConfig{Name: "my-slack", URL: "https://slack.com/mcp"}
		if installed, err := ops.InstallBundledProjection(dir, sc); installed != "" || err != nil {
			t.Errorf("InstallBundledProjection = %q, %v, want \"\" and no error for an existing file", installed, err)
		}
		got, _ := os.ReadFile(dest)
		if string(got) != string(original) {
			t.Errorf("existing projection was overwritten; got %q", got)
		}
	})

	t.Run("a write that fails for another reason is reported", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "servers"), "not a directory\n")
		sc := config.ServerConfig{Name: "my-github", URL: "https://api.github.com/mcp"}
		if installed, err := ops.InstallBundledProjection(dir, sc); err == nil {
			t.Errorf("InstallBundledProjection = %q, nil, want an error when servers/ can't hold the file", installed)
		}
	})
}

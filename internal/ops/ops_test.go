//go:build test

package ops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func TestDetectProjectionKey(t *testing.T) {
	tests := []struct {
		name string
		sc   config.ServerConfig
		want string
	}{
		{"github url", config.ServerConfig{URL: "https://api.github.com/mcp"}, "github"},
		{"githubcopilot url", config.ServerConfig{URL: "https://api.githubcopilot.com/mcp"}, "github"},
		{"github cmd", config.ServerConfig{Command: "npx", Args: []string{"server-github"}}, "github"},
		{"slack url", config.ServerConfig{URL: "https://slack.com/mcp"}, "slack"},
		{"slack cmd", config.ServerConfig{Command: "npx", Args: []string{"server-slack"}}, "slack"},
		{"jira url", config.ServerConfig{URL: "https://myco.atlassian.net/mcp"}, "jira"},
		{"linear url", config.ServerConfig{URL: "https://linear.app/mcp"}, "linear"},
		{"sentry url", config.ServerConfig{URL: "https://mcp.sentry.io"}, "sentry"},
		{"unknown", config.ServerConfig{URL: "https://example.com"}, ""},
		{"empty", config.ServerConfig{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ops.DetectProjectionKey(tt.sc)
			if got != tt.want {
				t.Errorf("DetectProjectionKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstallBundledProjection(t *testing.T) {
	t.Run("known server installs projection file", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "my-github", URL: "https://api.github.com/mcp"}
		ops.InstallBundledProjection(dir, sc)
		dest := filepath.Join(dir, "projections", "my-github.yaml")
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("projection file not created: %v", err)
		}
	})

	t.Run("unknown server installs nothing", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "unknown", URL: "https://example.com"}
		ops.InstallBundledProjection(dir, sc)
		dest := filepath.Join(dir, "projections", "unknown.yaml")
		if _, err := os.Stat(dest); err == nil {
			t.Fatal("expected no projection for unknown server")
		}
	})

	t.Run("existing file is not overwritten", func(t *testing.T) {
		dir := tempDir(t)
		projDir := filepath.Join(dir, "projections")
		os.MkdirAll(projDir, 0700)
		dest := filepath.Join(projDir, "my-slack.yaml")
		original := []byte("# custom\n")
		os.WriteFile(dest, original, 0600)
		sc := config.ServerConfig{Name: "my-slack", URL: "https://slack.com/mcp"}
		ops.InstallBundledProjection(dir, sc)
		got, _ := os.ReadFile(dest)
		if string(got) != string(original) {
			t.Errorf("existing projection was overwritten; got %q", got)
		}
	})
}

func TestWriteServer(t *testing.T) {
	t.Run("valid server creates yaml", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"server-github"}}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		path := filepath.Join(dir, "servers", "gh.yaml")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("server file not created: %v", err)
		}
	})

	t.Run("file has 0600 permissions", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "sec", Command: "run"}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		info, _ := os.Stat(filepath.Join(dir, "servers", "sec.yaml"))
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("perm = %04o, want 0600", perm)
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		err := ops.WriteServer(dir, config.ServerConfig{Name: "bad name!"})
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})

	t.Run("known server installs projection", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "gh", URL: "https://api.github.com/mcp", Transport: "http"}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		dest := filepath.Join(dir, "projections", "gh.yaml")
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("bundled projection not installed: %v", err)
		}
	})

	t.Run("omitempty fields absent from yaml", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "http-only", URL: "https://example.com", Transport: "http"}
		if err := ops.WriteServer(dir, sc); err != nil {
			t.Fatalf("WriteServer: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "servers", "http-only.yaml"))
		content := string(data)
		for _, unwanted := range []string{"command:", "args:", "env:"} {
			if strings.Contains(content, unwanted) {
				t.Errorf("yaml contains %q for empty field", unwanted)
			}
		}
	})
}

func TestDeleteServer(t *testing.T) {
	t.Run("deletes existing server", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "toremove", Command: "run"}
		ops.WriteServer(dir, sc) //nolint:errcheck
		if err := ops.DeleteServer(dir, "toremove"); err != nil {
			t.Fatalf("DeleteServer: %v", err)
		}
		path := filepath.Join(dir, "servers", "toremove.yaml")
		if _, err := os.Stat(path); err == nil {
			t.Fatal("server file still exists after delete")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		dir := tempDir(t)
		err := ops.DeleteServer(dir, "ghost")
		if err == nil {
			t.Fatal("expected error deleting non-existent server")
		}
	})

	t.Run("invalid name returns error", func(t *testing.T) {
		dir := tempDir(t)
		err := ops.DeleteServer(dir, "bad name!")
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

func TestPurgeExpiredResponses(t *testing.T) {
	t.Run("no responses dir returns zero", func(t *testing.T) {
		dir := tempDir(t)
		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 0 || freed != 0 {
			t.Errorf("expected 0, 0; got %d, %d", removed, freed)
		}
	})

	t.Run("removes expired pairs", func(t *testing.T) {
		dir := tempDir(t)
		respDir := filepath.Join(dir, "responses")
		os.MkdirAll(respDir, 0700)

		// Write an old response pair.
		oldJSON := filepath.Join(respDir, "old.json")
		oldRaw := filepath.Join(respDir, "old.raw.json")
		os.WriteFile(oldJSON, []byte(`{"ok":true}`), 0600)
		os.WriteFile(oldRaw, []byte(`{"raw":true}`), 0600)
		past := time.Now().Add(-30 * 24 * time.Hour)
		os.Chtimes(oldJSON, past, past)
		os.Chtimes(oldRaw, past, past)

		// Write a fresh response pair.
		freshJSON := filepath.Join(respDir, "fresh.json")
		os.WriteFile(freshJSON, []byte(`{"ok":true}`), 0600)

		removed, freed, err := ops.PurgeExpiredResponses(dir)
		if err != nil {
			t.Fatalf("PurgeExpiredResponses: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		if freed == 0 {
			t.Error("freed should be > 0")
		}
		if _, err := os.Stat(oldJSON); err == nil {
			t.Error("old.json still exists")
		}
		if _, err := os.Stat(freshJSON); err != nil {
			t.Error("fresh.json was removed")
		}
	})
}


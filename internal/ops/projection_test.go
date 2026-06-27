package ops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

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
		{"atlassian url", config.ServerConfig{URL: "https://myco.atlassian.net/mcp"}, "atlassian"},
		{"atlassian cmd", config.ServerConfig{Command: "uvx", Args: []string{"mcp-atlassian"}}, "atlassian"},
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
		dest := filepath.Join(dir, "servers", "my-github.proj.yaml")
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("projection file not created: %v", err)
		}
	})

	t.Run("unknown server installs nothing", func(t *testing.T) {
		dir := tempDir(t)
		sc := config.ServerConfig{Name: "unknown", URL: "https://example.com"}
		ops.InstallBundledProjection(dir, sc)
		dest := filepath.Join(dir, "servers", "unknown.proj.yaml")
		if _, err := os.Stat(dest); err == nil {
			t.Fatal("expected no projection for unknown server")
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
		ops.InstallBundledProjection(dir, sc)
		got, _ := os.ReadFile(dest)
		if string(got) != string(original) {
			t.Errorf("existing projection was overwritten; got %q", got)
		}
	})
}

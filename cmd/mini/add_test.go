package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/cmd/mini/importers"
)

func TestRunAdd(t *testing.T) {
	t.Run("stdio command creates server file", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "npx", "-y", "server-github"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "gh", &sc)
		if sc.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", sc.Command)
		}
		if len(sc.Args) != 2 || sc.Args[0] != "-y" {
			t.Errorf("Args = %v, want [-y server-github]", sc.Args)
		}
	})

	t.Run("--url flag creates http server", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--url", "https://api.github.com/mcp"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "gh", &sc)
		if sc.Transport != "http" {
			t.Errorf("Transport = %q, want 'http'", sc.Transport)
		}
		if sc.URL != "https://api.github.com/mcp" {
			t.Errorf("URL = %q, want 'https://api.github.com/mcp'", sc.URL)
		}
	})

	t.Run("--header flag is stored", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--url", "https://example.com", "--header", "Authorization=Bearer tok"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "svc", &sc)
		if sc.Headers["Authorization"] != "Bearer tok" {
			t.Errorf("Header Authorization = %q, want 'Bearer tok'", sc.Headers["Authorization"])
		}
	})

	t.Run("--protected flag marks tool", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--protected", "delete_everything", "run"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "svc", &sc)
		if sc.Permissions == nil || len(sc.Permissions.Protected) != 1 || sc.Permissions.Protected[0] != "delete_everything" {
			t.Errorf("Protected = %v, want [delete_everything]", sc.Permissions)
		}
	})

	t.Run("missing name returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error when no name given")
		}
	})

	t.Run("name but no url and no command returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{"myserver"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error when no url or command given")
		}
	})

	t.Run("invalid server name returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{"bad name!", "npx"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

func TestRunRemove(t *testing.T) {
	t.Run("removes existing server", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		runAdd(dir, []string{"myserver", "run"}, &out) //nolint:errcheck
		out.Reset()

		if err := runRemove(dir, []string{"myserver"}, &out); err != nil {
			t.Fatalf("runRemove: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "myserver.yaml")); err == nil {
			t.Fatal("server file still exists after remove")
		}
		if !strings.Contains(out.String(), "removed myserver") {
			t.Errorf("output = %q, want 'removed myserver'", out.String())
		}
	})

	t.Run("no args returns error", func(t *testing.T) {
		dir := t.TempDir()
		if err := runRemove(dir, []string{}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error when no name given")
		}
	})

	t.Run("non-existent server returns error", func(t *testing.T) {
		dir := t.TempDir()
		if err := runRemove(dir, []string{"ghost"}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error removing non-existent server")
		}
	})
}

func readServerYAML(t *testing.T, configDir, name string, out any) {
	t.Helper()
	path := filepath.Join(configDir, "servers", name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
}

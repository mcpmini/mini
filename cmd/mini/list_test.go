package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunList(t *testing.T) {
	t.Run("no servers configured prints message", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runList(dir, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(out.String(), "no servers configured") {
			t.Errorf("output = %q, want 'no servers configured'", out.String())
		}
	})

	t.Run("stdio server appears with correct transport and command", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "gh", "name: gh\ncommand: npx\nargs: [server-github]\n")

		var out bytes.Buffer
		if err := runList(dir, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "gh") {
			t.Errorf("output missing server name 'gh': %q", got)
		}
		if !strings.Contains(got, "stdio") {
			t.Errorf("output missing transport 'stdio': %q", got)
		}
		if !strings.Contains(got, "npx") {
			t.Errorf("output missing command 'npx': %q", got)
		}
	})

	t.Run("http server shows url and transport", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "remote", "name: remote\ntransport: http\nurl: https://example.com/mcp\n")

		var out bytes.Buffer
		if err := runList(dir, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "http") {
			t.Errorf("output missing transport 'http': %q", got)
		}
		if !strings.Contains(got, "https://example.com/mcp") {
			t.Errorf("output missing url: %q", got)
		}
	})

	t.Run("header row is always present", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "s", "name: s\ncommand: run\n")

		var out bytes.Buffer
		runList(dir, &out) //nolint:errcheck
		got := out.String()
		for _, col := range []string{"NAME", "TRANSPORT", "COMMAND / URL", "ENABLED"} {
			if !strings.Contains(got, col) {
				t.Errorf("output missing header column %q: %q", col, got)
			}
		}
	})
}

func writeServer(t *testing.T, configDir, name, yaml string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatalf("writeServer: %v", err)
	}
}

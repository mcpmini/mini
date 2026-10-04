package configtest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestWriteServerRoundTripsThroughLoader(t *testing.T) {
	dir := t.TempDir()
	disabled := false
	want := config.ServerConfig{
		Name:    "fixture_server",
		Command: "mini",
		Args:    []string{"--label=a b", "quote'and\"punct"},
		Enabled: &disabled,
	}

	WriteServer(t, dir, want)
	got, err := config.LoadServer(dir, want.Name)
	if err != nil {
		t.Fatalf("LoadServer(%q): %v", want.Name, err)
	}
	assertLoadedServer(t, got, want)
	assertOnlyServerFile(t, dir, want.Name)
}

func assertLoadedServer(t *testing.T, got, want config.ServerConfig) {
	t.Helper()
	if got.Name != want.Name {
		t.Errorf("loaded name = %q, want filename identity %q", got.Name, want.Name)
	}
	if got.Command != want.Command {
		t.Errorf("loaded command = %q, want %q", got.Command, want.Command)
	}
	if !reflect.DeepEqual(got.Args, want.Args) {
		t.Errorf("loaded args = %#v, want %#v", got.Args, want.Args)
	}
	if got.Enabled == nil || *got.Enabled {
		t.Errorf("loaded enabled = %v, want explicit false", got.Enabled)
	}
}

func assertOnlyServerFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "servers"))
	if err != nil {
		t.Fatalf("read server directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != name+".yaml" {
		t.Errorf("writer created server files %v, want only %s.yaml", entries, name)
	}
}

func TestWriteServerDoesNotInstallBundledFiles(t *testing.T) {
	dir := t.TempDir()
	WriteServer(t, dir, config.ServerConfig{Name: "gh", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"})
	assertOnlyServerFile(t, dir, "gh")
}

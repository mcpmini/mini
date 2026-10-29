//go:build test

package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestAuthSetupKeepsDefaultsWhenLoginFails(t *testing.T) {
	mini := miniCLI{bin: filepath.Join(t.TempDir(), "missing-mini"), configDir: t.TempDir()}
	if err := setUpAuth(
		scenario{Name: "auth/metadata-default", ServerURL: "http://localhost:1/mcp"},
		mini,
	); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadMain(mini.configDir)
	if err != nil {
		t.Fatal(err)
	}
	want := config.DefaultConfig()
	want.BrowserCommand = loginBrowserCommand
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auth config = %+v, want defaults with the suite browser command: %+v", got, want)
	}
}

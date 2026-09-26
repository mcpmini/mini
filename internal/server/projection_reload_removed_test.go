//go:build test

package server_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProjectionReload_serverYAMLDeletedKeepsProjections verifies that when
// mini rm deletes servers/<name>.yaml, a connected upstream retains its
// live projections rather than exposing its full unfiltered payload.
func TestProjectionReload_serverYAMLDeletedKeepsProjections(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a, b]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})
}

// TestProjectionReload_serverStillConfiguredProjFileDeletedClearsProjections
// verifies that deleting a server's .proj.yaml while the server itself remains
// configured clears the projections — the configured-on-disk state takes effect.
func TestProjectionReload_serverStillConfiguredProjFileDeletedClearsProjections(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.proj.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	e.assertDataKeys([]string{"a", "b", "secret"}, nil)
}

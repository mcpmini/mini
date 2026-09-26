//go:build test

package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectionReload_serverYAMLDeletedByRm_connectedUpstreamStaysProjected(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a, b]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})
}

func TestProjectionReload_neverConnectedServerRemoved_leavesNoProjectionEntry(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"), "name: svc\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), "getData:\n  include_only: [a]\n")
	env := buildReloadEnv(t, dir)
	env.startPoller()

	if err := os.Remove(filepath.Join(dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	env.advanceTick()

	resp := serve(t, env.srv, callTool("config", map[string]any{"action": "status"}))
	var status map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &status); err != nil {
		t.Fatalf("status response not JSON: %v", err)
	}
	if projections, _ := status["projections"].(map[string]any); projections["svc"] != nil {
		t.Errorf("expected no projections for a removed server that never connected, got %v", projections)
	}
}

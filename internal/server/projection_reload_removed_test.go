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

func TestProjectionReload_serverYAMLDeletedEditProjFileTakesEffect(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a, b]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	e.writeProjFile("getData:\n  include_only: [a]\n")
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_serverYAMLDeletedThenProjFileDeletedKeepsLiveProjections(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a, b]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.proj.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})
}

func TestProjectionReload_configYAMLDeletedKeepsProjectionsForConnectedServer(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [a]\n")
	env := buildReloadEnv(t, dir)
	addReloadUpstream(t, env.srv)
	env.startPoller()
	env.assertDataKeys([]string{"a"}, []string{"b", "secret"})

	if err := os.Remove(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	env.advanceTick()

	env.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_neverConnectedServerRemovedLeavesNoProjectionEntry(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"), "name: svc\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), "getData:\n  include_only: [a]\n")
	env := buildReloadEnv(t, dir)
	env.startPoller()

	if err := os.Remove(filepath.Join(dir, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "servers", "svc.proj.yaml")); err != nil {
		t.Fatal(err)
	}
	env.advanceTick()

	resp := serve(t, env.srv, callTool("config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status response not JSON: %s", text)
	}
	projections, _ := status["projections"].(map[string]any)
	if _, hasSvc := projections["svc"]; hasSvc {
		t.Errorf("expected no projections for never-connected removed server, got: %v", projections)
	}
}

func TestNewWithConfigDir_leftoverProjFileWithoutServer_notApplied(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), "getData:\n  include_only: [a]\n")
	e := buildReloadEnv(t, dir)
	addReloadUpstream(t, e.srv)

	e.assertDataKeys([]string{"a", "b", "secret"}, nil)
}

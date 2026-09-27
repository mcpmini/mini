//go:build test

package server_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func addReloadUpstreamNamed(t *testing.T, srv *server.Server, name string) {
	t.Helper()
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: name}, fake); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionReloadIsolation_oneBadServerFileDoesNotBlockOther(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "a.yaml"), "name: a\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a, b]\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "b.yaml"), "name: b\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "b.proj.yaml"), "getData:\n  include_only: [a, b]\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e.srv, "a")
	addReloadUpstreamNamed(t, e.srv, "b")
	e.startPoller()

	writeReloadFile(t, filepath.Join(dir, "servers", "b.proj.yaml"), "getData: [bad yaml\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a]\n")
	e.advanceTick()

	if logs := e.logs.String(); !strings.Contains(logs, "projection reload: skipped server") || !strings.Contains(logs, "server=b") {
		t.Errorf("expected skipped-server WARN for b, got logs:\n%s", logs)
	}
	e.assertServerDataKeys("a", []string{"a"}, []string{"b"})
	e.assertServerDataKeys("b", []string{"a", "b"}, []string{"secret"})
}

func TestProjectionReloadIsolation_sourceErrorKeepsLiveProjectionOverInlineTwin(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"),
		"name: svc\ncommand: echo\nprojections:\n  getData:\n    include_only: [a]\n")
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [b]\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e.srv, "svc")
	e.startPoller()

	e.assertServerDataKeys("svc", []string{"a"}, []string{"b", "secret"})

	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"), "bad: [yaml\n")
	e.advanceTick()

	if !strings.Contains(e.logs.String(), "projection reload: source error") {
		t.Errorf("expected source-error WARN, got logs:\n%s", e.logs.String())
	}
	e.assertServerDataKeys("svc", []string{"a"}, []string{"b", "secret"})
}

//go:build test

package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func addReloadUpstreamNamed(t *testing.T, e *reloadEnv, name string) {
	t.Helper()
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`)
	if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: name}, fake); err != nil {
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
	addReloadUpstreamNamed(t, e, "a")
	addReloadUpstreamNamed(t, e, "b")
	e.startPoller()

	writeReloadFile(t, filepath.Join(dir, "servers", "b.proj.yaml"), "getData: [bad yaml\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a]\n")
	e.advanceTick()

	if logs := e.logs.String(); !strings.Contains(logs, "projection reload: skipped server") {
		t.Errorf("expected WARN for skipped server b, got logs:\n%s", logs)
	}
	if !strings.Contains(e.logs.String(), "server=b") {
		t.Errorf("expected skipped server name b in logs:\n%s", e.logs.String())
	}

	resp := serve(t, e.srv, callTool("call", map[string]any{"server": "a", "tool": "getData", "params": map[string]any{}}))
	dataA := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if dataA["a"] == nil {
		t.Errorf("a: new projection not applied, got %v", dataA)
	}
	if dataA["b"] != nil {
		t.Errorf("a: old include [a,b] still active, got %v", dataA)
	}

	resp = serve(t, e.srv, callTool("call", map[string]any{"server": "b", "tool": "getData", "params": map[string]any{}}))
	dataB := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if dataB["a"] == nil || dataB["b"] == nil {
		t.Errorf("b: previous projection should be kept after bad proj.yaml, got %v", dataB)
	}
	if dataB["secret"] != nil {
		t.Errorf("b: projection lost entirely (secret exposed), got %v", dataB)
	}
}

func TestProjectionReloadIsolation_undefinedEnvVarInServerFileDoesNotBlock(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "a.yaml"), "name: a\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a, b]\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "c.yaml"),
		"name: c\nurl: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_VAR_XYZ}\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e, "a")
	e.startPoller()

	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a]\n")
	e.advanceTick()

	resp := serve(t, e.srv, callTool("call", map[string]any{"server": "a", "tool": "getData", "params": map[string]any{}}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("a's new projection not applied despite undefined env var in c, got %v", data)
	}
	if data["b"] != nil {
		t.Errorf("a's old projection still active, got %v", data)
	}
}

func TestProjectionReloadIsolation_brokenConfigYAMLKeepsInlineProjection(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [a]\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e, "svc")
	e.startPoller()

	resp := serve(t, e.srv, callTool("call", map[string]any{"server": "svc", "tool": "getData", "params": map[string]any{}}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("initial inline projection not applied: %v", data)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bad: [yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	resp = serve(t, e.srv, callTool("call", map[string]any{"server": "svc", "tool": "getData", "params": map[string]any{}}))
	data = parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("inline projection should be kept when config.yaml is broken, got %v", data)
	}
	if data["secret"] != nil {
		t.Errorf("projection lost entirely on broken config.yaml (secret exposed), got %v", data)
	}
}

func TestProjectionReloadIsolation_brokenConfigYAMLLogsWarn(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "a.yaml"), "name: a\ncommand: echo\n")
	writeReloadFile(t, filepath.Join(dir, "servers", "a.proj.yaml"), "getData:\n  include_only: [a]\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e, "a")
	e.startPoller()

	writeReloadFile(t, filepath.Join(dir, "config.yaml"), "bad: [yaml\n")
	e.advanceTick()

	if !strings.Contains(e.logs.String(), "projection reload: source error") {
		t.Errorf("expected WARN with message 'projection reload: source error', got logs:\n%s", e.logs.String())
	}
}

func TestProjectionReloadIsolation_brokenServersFileKeepsLiveProjectionOverInlineTwin(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"),
		"name: svc\ncommand: echo\nprojections:\n  getData:\n    include_only: [a]\n")
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [b]\n")

	e := buildReloadEnv(t, dir)
	addReloadUpstreamNamed(t, e, "svc")
	e.startPoller()

	resp := serve(t, e.srv, callTool("call", map[string]any{"server": "svc", "tool": "getData", "params": map[string]any{}}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil || data["secret"] != nil {
		t.Fatalf("initial: expected only 'a' from servers/ file projection, got %v", data)
	}

	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"), "bad: [yaml\n")
	e.advanceTick()

	resp = serve(t, e.srv, callTool("call", map[string]any{"server": "svc", "tool": "getData", "params": map[string]any{}}))
	data = parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("live projection should be kept when servers/ file breaks, got %v", data)
	}
	if data["b"] != nil {
		t.Errorf("inline twin projection must NOT replace live projection, got %v", data)
	}
	if data["secret"] != nil {
		t.Errorf("projection lost entirely (secret exposed), got %v", data)
	}
}

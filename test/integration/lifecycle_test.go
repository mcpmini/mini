//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLifecycle_addServerAtRuntime(t *testing.T) {
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	writeConfig(t, cfg, "dangerous_allow_private_urls: true\n")
	client := startServer(t, cfg)

	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "svc", "transport": "sse", "url": f.srv.URL},
		},
	})
	var r struct{ IsError bool `json:"isError"` }
	json.Unmarshal(raw, &r) //nolint:errcheck
	if r.IsError {
		t.Fatalf("add_server failed: %s", raw)
	}

	if !strings.Contains(client.listTools("svc"), "get_item") {
		t.Error("expected get_item in list after add_server")
	}
}

func TestLifecycle_removeServerAtRuntime(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	client := startServer(t, cfg)

	if !strings.Contains(client.listTools("svc"), "get_item") {
		t.Fatal("setup: expected get_item in list before remove")
	}

	client.mustCall("tools/call", map[string]any{
		"name":      "config",
		"arguments": map[string]any{"action": "remove_server", "server": "svc"},
	})

	_, isErr := client.execToolAllowError("svc", "get_item", nil)
	if !isErr {
		t.Error("expected error after server removed")
	}
}

func TestLifecycle_addServerBadURL(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "dangerous_allow_private_urls: true\n")
	client := startServer(t, cfg)

	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "bad", "transport": "sse", "url": "not-a-url"},
		},
	})
	var r struct{ IsError bool `json:"isError"` }
	json.Unmarshal(raw, &r) //nolint:errcheck
	if !r.IsError {
		t.Error("expected error for bad URL")
	}
}

func TestLifecycle_disabledServerNotLoaded(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "disabled", fmt.Sprintf(
		"name: disabled\ncommand: %s\nargs:\n  - --fixtures\n  - %s\nenabled: false\n",
		fakemcpBin, dir))

	client := startServer(t, cfg)
	_, isErr := client.execToolAllowError("disabled", "get_item", nil)
	if !isErr {
		t.Error("call to disabled server should return error")
	}
	if strings.Contains(client.listTools(""), "disabled") {
		t.Error("disabled server tools should not appear in list")
	}
}

func TestLifecycle_tenServersSimultaneously(t *testing.T) {
	cfg := t.TempDir()
	for i := range 10 {
		dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
		name := fmt.Sprintf("svc%d", i)
		writeServerConfig(t, cfg, name, fmt.Sprintf(
			"name: %s\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n", name, fakemcpBin, dir))
	}

	client := startServer(t, cfg)
	listing := client.listTools("")
	if !strings.Contains(listing, "svc0") || !strings.Contains(listing, "svc9") {
		t.Errorf("expected svc0..svc9 in listing, got: %s", listing[:min(200, len(listing))])
	}
}


//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

type actionServerParams struct {
	Fixtures map[string]string
	Action   config.ActionConfig
}

func actionServer(t *testing.T, p actionServerParams) *mcpClient {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, p.Fixtures)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	configtest.WriteAction(t, cfg, p.Action)
	return startServer(t, cfg)
}

func TestIntegrationActions_defaultArgsMergedWithCallArgs(t *testing.T) {
	t.Skip("actions not user-visible in v0.1")
	client := actionServer(t, actionServerParams{
		Fixtures: map[string]string{"get_item": `{"id":42,"name":"fetched"}`},
		Action: config.ActionConfig{
			Name:        "myfetch",
			Description: "Fetch",
			Server:      "svc",
			Tool:        "get_item",
			DefaultArgs: map[string]any{"id": 42, "extra": "default"},
		},
	})

	e := client.execEnvelope("svc", "myfetch", map[string]any{"id": 99})
	if e.Error != "" {
		t.Errorf("action with call-time args overriding default should succeed, got: %+v", e)
	}
}

func TestIntegrationActions_protectedActionRequiresExecProtected(t *testing.T) {
	t.Skip("actions not user-visible in v0.1")
	client := actionServer(t, actionServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1}`},
		Action: config.ActionConfig{
			Name:        "protected_fetch",
			Description: "Protected",
			Server:      "svc",
			Tool:        "get_item",
			Permission:  "protected",
		},
	})

	_, isErr := client.execToolAllowError("svc", "protected_fetch", nil)
	if !isErr {
		t.Error("call on protected action should fail")
	}
	_, isErr = client.execProtectedAllowError("svc", "protected_fetch", nil)
	if isErr {
		t.Error("perm_call on protected action should succeed")
	}
}

func TestIntegrationActions_badServerReference(t *testing.T) {
	client := actionServer(t, actionServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1}`},
		Action: config.ActionConfig{
			Name:        "broken",
			Description: "Bad server",
			Server:      "nonexistent",
			Tool:        "get_item",
		},
	})

	_, isErr := client.execToolAllowError("nonexistent", "broken", nil)
	if !isErr {
		t.Error("action referencing nonexistent server should return error")
	}
}

func TestIntegrationActions_execAction(t *testing.T) {
	t.Skip("actions not user-visible in v0.1")
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":42,"name":"fetched"}`})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	configtest.WriteAction(t, cfg, config.ActionConfig{
		Name:        "myfetch",
		Description: "Fetch item 42",
		Server:      "svc",
		Tool:        "get_item",
		DefaultArgs: map[string]any{"id": 42},
	})

	client := startServer(t, cfg)

	if !strings.Contains(client.listTools("svc"), "myfetch") {
		t.Error("expected action 'myfetch' to appear in list")
	}

	e := client.execEnvelope("svc", "myfetch", nil)
	if e.Error != "" {
		t.Errorf("expected ok=true from action call, got: %+v", e)
	}
}

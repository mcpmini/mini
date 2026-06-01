//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestExecProtectedOnOpenTool(t *testing.T) {
	srv := newEdgeServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeConn("doThing"))

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "svc", "tool": "doThing", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "call") {
		t.Errorf("error should mention call, got: %s", text)
	}
}

func TestExecOnHiddenTool_notFound(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("secret", "visible"))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "secret", "params": map[string]any{},
	}))
	requireRPCError(t, resp, transport.CodeInvalidParams, "not found")
}

func TestExecOnHiddenTool_notInDiscover(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("secret", "visible"))

	resp := serve(t, srv, callTool("list", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "secret") {
		t.Errorf("hidden tool should not appear in discover results, got: %s", text)
	}
	if !strings.Contains(text, "visible") {
		t.Errorf("visible tool should appear in discover results, got: %s", text)
	}
}

func newSvcWithPartialProjection(t *testing.T) *server.Server {
	t.Helper()
	srv := newEdgeServer(t)
	fake := fakeConn("coveredTool", "uncoveredTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "coveredTool",
		"projection": map[string]any{"depth_limit": 3},
	}))
	return srv
}

func TestCallBlockedWhenProjectionFileExistsButToolUncovered(t *testing.T) {
	srv := newSvcWithPartialProjection(t)

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "coveredTool"}))
	if text := toolResultText(t, resp); strings.Contains(text, "no projection") {
		t.Errorf("covered tool should succeed via call, got: %s", text)
	}

	resp = serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "uncoveredTool"}))
	if text := toolResultText(t, resp); !strings.Contains(text, "perm_call") {
		t.Errorf("uncovered tool: expected perm_call suggestion, got: %s", text)
	}
}

func TestPermCallAllowsUncoveredTool(t *testing.T) {
	srv := newSvcWithPartialProjection(t)

	resp := serve(t, srv, callTool("perm_call", map[string]any{"server": "svc", "tool": "uncoveredTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "isError") || strings.Contains(text, "perm_call") {
		t.Errorf("uncovered tool should succeed via perm_call, got: %s", text)
	}
}

func TestWildcardProjectionGrantsCallCoverage(t *testing.T) {
	srv := newEdgeServer(t)
	fake := fakeConn("anyTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "*",
		"projection": map[string]any{"depth_limit": 3},
	}))

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "anyTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "no projection") {
		t.Errorf("wildcard should cover anyTool, got: %s", text)
	}
}

func newSvcWithReadOnlyTool(t *testing.T) *server.Server {
	t.Helper()
	srv := newEdgeServer(t)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "covered", ReadOnly: false},
			{Name: "readOnlyGet", ReadOnly: true},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"data"}]}`),
		},
	}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "covered",
		"projection": map[string]any{"depth_limit": 3},
	}))
	return srv
}

func TestReadOnlyToolRequiresProjectionCoverage(t *testing.T) {
	srv := newSvcWithReadOnlyTool(t)
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "readOnlyGet"}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "no projection") {
		t.Errorf("read-only tools must also require projection coverage, got: %s", text)
	}
}

func TestNoProjectionFileAllowsCallNormally(t *testing.T) {
	srv := newEdgeServer(t)
	fake := fakeConn("anyTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "anyTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "no projection") {
		t.Errorf("server without any projections should allow call, got: %s", text)
	}
}

func TestDefaultPermissionProtected(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Default: "protected"}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("dangerousOp", "safeRead"))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "dangerousOp", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "perm_call") {
		t.Errorf("expected perm_call mention for default-protected tool, got: %s", text)
	}
}

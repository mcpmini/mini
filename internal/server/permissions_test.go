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

func TestExecOnHiddenTool_notFound(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(
		addConnectionParams{
			T:          t,
			Server:     srv,
			Config:     config.ServerConfig{Name: "svc", Permissions: perm},
			Connection: fakeConn("secret", "visible"),
		},
	)

	resp := serve(t, srv, callTool("svc__secret", map[string]any{}))
	requireRPCError(
		requireRPCErrorParams{T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found"},
	)
}

func TestExecOnHiddenTool_notInDiscover(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(
		addConnectionParams{
			T:          t,
			Server:     srv,
			Config:     config.ServerConfig{Name: "svc", Permissions: perm},
			Connection: fakeConn("secret", "visible"),
		},
	)

	resp := serve(t, srv, rpc("tools/list", nil))
	text := resultJSONText(t, resp)
	if strings.Contains(text, "secret") {
		t.Errorf("hidden tool should not appear in discover results, got: %s", text)
	}
	if !strings.Contains(text, "visible") {
		t.Errorf("visible tool should appear in discover results, got: %s", text)
	}
}

func TestNoProjectionFileAllowsCallNormally(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeConn("anyTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fake})

	resp := serve(t, srv, callTool("svc__anyTool", nil))
	text := toolResultText(t, resp)
	if strings.Contains(text, "no projection") {
		t.Errorf("server without any projections should allow call, got: %s", text)
	}
}

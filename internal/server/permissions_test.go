//go:build test

package server_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestCallOnHiddenToolIsNotFound(t *testing.T) {
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

func TestHiddenToolIsNotListed(t *testing.T) {
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
		t.Errorf("hidden tool should not appear in tools/list, got: %s", text)
	}
	if !strings.Contains(text, "visible") {
		t.Errorf("visible tool should appear in tools/list, got: %s", text)
	}
}

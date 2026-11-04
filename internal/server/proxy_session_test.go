//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type postMCPParams struct {
	T         *testing.T
	Server    *server.Server
	SessionID string
	Msg       any
}

func postMCP(params postMCPParams) map[string]any {
	params.T.Helper()
	b, _ := json.Marshal(params.Msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", params.SessionID)
	w := httptest.NewRecorder()
	params.Server.ServeHTTP(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	return resp
}

func initMsg() map[string]any {
	params := map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	return map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 0, "method": "initialize", "params": params}
}

func TestProxy_SessionProjection_FieldExclusionPersistsAcrossCalls(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"topsecret","name":"foo"}`), "get_item")
	session := h.session("ffffffff-ffff-ffff-ffff-ffffffffffff")

	session.setProjection("svc", "get_item", map[string]any{"exclude": []string{"secret"}})

	for i := range 3 {
		if text := session.callText("svc__get_item", nil); strings.Contains(text, "topsecret") {
			t.Errorf("call %d: secret field leaked in response: %s", i+1, text)
		}
	}
}

func TestProxy_SessionProjection_IsolatedBetweenSessions(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"topsecret","name":"foo"}`), "get_item")
	sessionA := h.session("aaaaaaaa-aaaa-aaaa-aaaa-000000000001")
	sessionB := h.session("bbbbbbbb-bbbb-bbbb-bbbb-000000000002")

	sessionA.setProjection("svc", "get_item", map[string]any{"exclude": []string{"secret"}})

	if strings.Contains(sessionA.callText("svc__get_item", nil), "topsecret") {
		t.Error("session A: projection should exclude secret")
	}
	if !strings.Contains(sessionB.callText("svc__get_item", nil), "topsecret") {
		t.Error("session B: should see full response (no projection set)")
	}
}

func TestProxy_Reload_PreservesSessionProjections(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"topsecret"}`), "get_item")
	session := h.session("cccccccc-cccc-cccc-cccc-000000000003")
	session.setProjection("svc", "get_item", map[string]any{"exclude": []string{"secret"}})

	successText(t, session.call("config", map[string]any{"action": "reload"}))

	if text := session.callText("svc__get_item", nil); strings.Contains(text, "topsecret") {
		t.Errorf("session projection wiped by reload: %s", text)
	}
}

//go:build test

package server_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestModernMetadataRejectedBeforeActivationOrDispatch(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeProxy})
	conn := fakeConn("ping")
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	callParams := json.RawMessage(`{"name":"svc__ping","arguments":{},"_meta":null}`)
	lines := [][]byte{
		rawRequest(t, rawRequestParams{
			ID: 1, Method: "server/discover",
			Params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`,
		}),
		rawRequest(t, rawRequestParams{
			ID: 2, Method: "tools/list",
			Params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":[]}}`,
		}),
		rawRequest(t, rawRequestParams{ID: 3, Method: "tools/call", Params: string(callParams)}),
		legacyRequest(legacyRequestParams{T: t, ID: 4, Method: "initialize", Params: initParams(true)}),
		legacyRequest(legacyRequestParams{T: t, ID: 5, Method: "tools/list", Params: nil}),
	}
	responses := responsesByID(serveModern(t, srv, lines...))
	for _, id := range []int{1, 2, 3} {
		if responses[id].Error == nil || responses[id].Error.Code != transport.CodeInvalidParams {
			t.Errorf("request %d error = %+v, want -32602", id, responses[id].Error)
		}
	}
	if got := len(decodeResult[transport.ToolsListResult](t, responses[5]).Tools); got != 4 {
		t.Fatalf("legacy tools/list after invalid probes returned %d tools, want compact mode's 4", got)
	}
	if len(conn.LastParams) != 0 {
		t.Fatalf("invalid tools/call reached upstream with params %s", conn.LastParams)
	}
}

func TestLegacyListenWithUnrelatedMetadataRemainsMethodNotFound(t *testing.T) {
	request := rawRequest(t, rawRequestParams{
		ID: 1, Method: transport.MethodSubscriptionsListen,
		Params: `{"_meta":{"trace":"legacy"}}`,
	})
	messages := serveAll(t, newTestServer(t, server.Params{}), request)
	if got := rpcErrorCode(messages[len(messages)-1]); got != transport.CodeMethodNotFound {
		t.Fatalf("legacy listen error code = %v, want -32601", got)
	}
}

type rawRequestParams struct {
	ID     int
	Method string
	Params string
}

func rawRequest(t *testing.T, p rawRequestParams) []byte {
	t.Helper()
	request := transport.Request{
		JsonRPC: transport.JsonRPCVersion,
		ID:      p.ID,
		Method:  p.Method,
		Params:  json.RawMessage(p.Params),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestPerSession_rpcErrorKeepsConn(t *testing.T) {
	var dialCount int
	rpcErrOnCall := 2 // inject RPC error on the 2nd tools/call
	upstreamSrv := perSessionHTTPServer(t, &dialCount, &rpcErrOnCall)
	ts := httptest.NewServer(newPerSessionSrv(t, upstreamSrv.URL))
	t.Cleanup(ts.Close)
	baseline := dialCount
	sid := "aabbccdd11223344aabbccdd11223344"

	init, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "0"},
		},
	})
	exec, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "svc__ping", "arguments": map[string]any{}},
	})
	postToMiniHTTP(mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: init})
	postToMiniHTTP(
		mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec},
	) // call 1: success, triggers per-session dial
	postToMiniHTTP(mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec}) // call 2: RPC error
	postToMiniHTTP(
		mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec},
	) // call 3: should reuse conn, not redial

	if dialCount != baseline+1 {
		t.Errorf("RPC error should not redial per-session conn: expected %d dials, got %d", baseline+1, dialCount)
	}
}

// TestPerSession_transportErrorRedialsConn verifies that a transport-level
// error (abrupt connection close, not an RPC error) evicts the per-session
// conn so the next call re-dials instead of reusing a broken connection.
func TestPerSession_transportErrorRedialsConn(t *testing.T) {
	var dialCount, closeOnCall, callsSeen atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			dialCount.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, //nolint:errcheck
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
				}})
		case "tools/list":
			tools := []map[string]any{
				{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
			}
			json.NewEncoder(w).
				Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": map[string]any{"tools": tools}})
		//nolint:errcheck
		case "tools/call":
			n := callsSeen.Add(1)
			if c := closeOnCall.Load(); c > 0 && n == c {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Error("hijack not supported")
					return
				}
				conn, _, _ := hj.Hijack()
				conn.Close() // abrupt transport close
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, //nolint:errcheck
				"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}})
		}
	})
	upstreamSrv := httptest.NewServer(handler)
	t.Cleanup(upstreamSrv.Close)
	ts := httptest.NewServer(newPerSessionSrv(t, upstreamSrv.URL))
	t.Cleanup(ts.Close)

	sid := "aabbccdd11223344aabbccdd11223344"
	init, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "0"},
		},
	})
	exec, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "svc__ping", "arguments": map[string]any{}},
	})

	postToMiniHTTP(mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: init})
	postToMiniHTTP(
		mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec},
	) // call 1: success, dials per-session conn (dialCount → 1)

	closeOnCall.Store(2)
	postToMiniHTTP(
		mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec},
	) // call 2: transport error → conn evicted

	closeOnCall.Store(0)
	postToMiniHTTP(
		mcpPostParams{T: t, HTTPServer: ts, SessionID: sid, Body: exec},
	) // call 3: should redial (dialCount → 2)

	if got := dialCount.Load(); got < 2 {
		t.Errorf("transport error should trigger redial: expected ≥2 dials, got %d", got)
	}
}

func TestPerSession_aConnectionThatFailsToListToolsIsNotKeptForTheSession(t *testing.T) {
	var dials, failListOnDial atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("upstream got a malformed request: %v", err)
			return
		}
		reply := map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": req["id"]}
		switch req["method"] {
		case "initialize":
			dials.Add(1)
			reply["result"] = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			}
		case "tools/list":
			if dials.Load() == failListOnDial.Load() {
				reply["error"] = map[string]any{"code": -32603, "message": "not ready"}
				break
			}
			reply["result"] = map[string]any{"tools": []map[string]any{
				{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			reply["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		default:
			return
		}
		json.NewEncoder(w).Encode(reply) //nolint:errcheck // A failed write surfaces as the call's result.
	})
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	ts := httptest.NewServer(newPerSessionSrv(t, upstream.URL))
	t.Cleanup(ts.Close)
	sid := initSession(t, ts)
	failListOnDial.Store(dials.Load() + 1)
	call := httpExecToolTextParams{T: t, HTTPServer: ts, SessionID: sid, ServerName: "svc", Tool: "ping"}

	first := httpExecToolText(call)
	second := httpExecToolText(call)

	if first == "pong" {
		t.Fatalf("first call succeeded although its per-session connection failed tools/list")
	}
	if second != "pong" {
		t.Errorf("second call = %q, want pong", second)
	}
	if got, want := dials.Load(), failListOnDial.Load()+1; got != want {
		t.Errorf(
			"upstream saw %d dials, want %d: the second call must dial again, not reuse the failed connection",
			got,
			want,
		)
	}
}

func perSessionHTTPServer(t *testing.T, dialCount *int, rpcErrOnCall *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(perSessionUpstreamHandler(dialCount, rpcErrOnCall))
	t.Cleanup(srv.Close)
	return srv
}

func perSessionUpstreamHandler(dialCount *int, rpcErrOnCall *int) http.HandlerFunc {
	callsSeen := 0
	return func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			*dialCount++
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, //nolint:errcheck
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
				}})
		case "tools/list":
			tools := []map[string]any{
				{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
			}
			json.NewEncoder(w).
				Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": map[string]any{"tools": tools}})
		//nolint:errcheck
		case "tools/call":
			callsSeen++
			writeToolsCallResp(
				writeToolsCallRespParams{Writer: w, ID: id, CallsSeen: callsSeen, RPCErrorOnCall: rpcErrOnCall},
			)
		}
	}
}

func writeToolsCallResp(params writeToolsCallRespParams) {
	if *params.RPCErrorOnCall > 0 && params.CallsSeen == *params.RPCErrorOnCall {
		json.NewEncoder(params.Writer).
			Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": params.ID, "error": map[string]any{"code": -32602, "message": "bad args"}})

			//nolint:errcheck
		return
	}
	json.NewEncoder(params.Writer).
		Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": params.ID, //nolint:errcheck
			"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}})
}

type writeToolsCallRespParams struct {
	Writer         http.ResponseWriter
	ID             any
	CallsSeen      int
	RPCErrorOnCall *int
}

func newPerSessionSrv(t *testing.T, url string) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: url, SessionMode: config.SessionModePerSession}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	return srv
}

func postToMiniHTTP(params mcpPostParams) {
	params.T.Helper()
	req, _ := http.NewRequest(http.MethodPost, params.HTTPServer.URL+"/mcp", bytes.NewReader(params.Body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", params.SessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		params.T.Fatalf("mini POST: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
}

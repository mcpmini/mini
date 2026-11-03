//go:build test

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
)

var (
	pongResult  = mcptest.Reply{Result: map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}}
	pongMethods = map[string]mcptest.Method{"tools/call": func(mcptest.Request) mcptest.Reply { return pongResult }}
)

func TestPerSession_rpcErrorKeepsConn(t *testing.T) {
	var calls atomic.Int32
	up := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools, Methods: map[string]mcptest.Method{
		"tools/call": func(mcptest.Request) mcptest.Reply {
			if calls.Add(1) == 2 {
				return mcptest.Reply{Error: &mcptest.Error{Code: -32602, Message: "bad args"}}
			}
			return pongResult
		},
	}})
	call := pingCall(t, servePerSession(t, up.URL))
	baseline := dials(up)

	httpExecToolText(call) // dials the session's connection
	httpExecToolText(call) // RPC error
	httpExecToolText(call)

	if got := dials(up); got != baseline+1 {
		t.Errorf("RPC error should not redial per-session conn: expected %d dials, got %d", baseline+1, got)
	}
}

// TestPerSession_transportErrorRedialsConn verifies that a transport-level
// error (abrupt connection close, not an RPC error) evicts the per-session
// conn so the next call re-dials instead of reusing a broken connection.
func TestPerSession_transportErrorRedialsConn(t *testing.T) {
	var dropNext atomic.Bool
	dropOnce := func(w http.ResponseWriter, _ *http.Request) bool {
		return dropNext.CompareAndSwap(true, false) && dropConn(t, w)
	}
	up := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools, Methods: pongMethods, Intercept: dropOnce})
	call := pingCall(t, servePerSession(t, up.URL))
	httpExecToolText(call) // dials the session's connection
	before := dials(up)

	dropNext.Store(true)
	httpExecToolText(call) // transport error evicts the connection
	httpExecToolText(call)

	if got := dials(up); got <= before {
		t.Errorf("transport error should trigger redial: expected more than %d dials, got %d", before, got)
	}
}

func TestPerSession_aConnectionThatFailsToListToolsIsNotKeptForTheSession(t *testing.T) {
	var failListOnDial atomic.Int32
	var up *mcptest.Server
	up = mcptest.NewHTTP(t, mcptest.Upstream{Methods: map[string]mcptest.Method{
		"tools/list": func(mcptest.Request) mcptest.Reply {
			if dials(up) == failListOnDial.Load() {
				return mcptest.Reply{Error: &mcptest.Error{Code: -32603, Message: "not ready"}}
			}
			return mcptest.Reply{Result: map[string]any{"tools": pingTools}}
		},
		"tools/call": pongMethods["tools/call"],
	}})
	call := pingCall(t, servePerSession(t, up.URL))
	failListOnDial.Store(dials(up) + 1)

	first := httpExecToolText(call)
	second := httpExecToolText(call)

	if first == "pong" {
		t.Fatalf("first call succeeded although its per-session connection failed tools/list")
	}
	if second != "pong" {
		t.Errorf("second call = %q, want pong", second)
	}
	if got, want := dials(up), failListOnDial.Load()+1; got != want {
		t.Errorf(
			"upstream saw %d dials, want %d: the second call must dial again, not reuse the failed connection",
			got,
			want,
		)
	}
}

func servePerSession(t *testing.T, url string) *httptest.Server {
	t.Helper()
	srv, mini := newHTTPTestServer(t, server.Params{})
	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: url, SessionMode: config.SessionModePerSession}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	return mini
}

func pingCall(t *testing.T, mini *httptest.Server) httpExecToolTextParams {
	t.Helper()
	sid := initSession(t, mini)
	return httpExecToolTextParams{T: t, HTTPServer: mini, SessionID: sid, ServerName: "svc", Tool: "ping"}
}

func dials(up *mcptest.Server) int32 {
	var n int32
	for _, req := range up.Requests() {
		if req.Method == "initialize" {
			n++
		}
	}
	return n
}

func dropConn(t *testing.T, w http.ResponseWriter) bool {
	t.Helper()
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return false
	}
	conn.Close() //nolint:errcheck,gosec // the abrupt close is the point
	return true
}

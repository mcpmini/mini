//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

var arrayLimit = map[string]any{"array_limits": map[string]any{"default": 2}}

func jsonText(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func newProxyServer(t *testing.T, upstream string, projection map[string]any) (srv *server.Server, responseDir string) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv = newTestServer(t, server.Params{Config: cfg})
	t.Cleanup(srv.Close)
	conn := fakeConn("tool")
	conn.Responses["tools/call"] = json.RawMessage(upstream)
	addProxyConn(t, srv, "svc", conn)
	if projection != nil {
		serveProxy(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "svc", "tool": "tool", "projection": projection,
		}))
	}
	return srv, cfg.ResponseDir
}

func callProxyTool(t *testing.T, srv *server.Server, args map[string]any) map[string]any {
	t.Helper()
	resp := serveProxy(t, srv, callTool("svc__tool", args))
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	return result
}

func decodeJSON(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return v
}

func TestProxyResult_aTrimmedResultIsRecoverableWithRead(t *testing.T) {
	srv, _ := newProxyServer(t, `{"content":[{"type":"text","text":"[1,2,3,4]"}]}`, arrayLimit)
	content := callProxyTool(t, srv, map[string]any{})["content"].([]any)
	if got := content[0].(map[string]any)["text"]; got != "[1,2]" {
		t.Errorf("trimmed text = %v, want [1,2]", got)
	}
	notice := decodeJSON(t, content[1].(map[string]any)["text"].(string)).(map[string]any)["__mini"].(map[string]any)
	path := notice["truncated"].([]any)[0].(map[string]any)["path"].(string)
	resp := serveProxy(t, srv, callTool("read", map[string]any{"file": notice["file"], "filter": path}))
	assertDeepEqual(t, decodeJSON(t, toolResultText(t, resp)), []any{1.0, 2.0, 3.0, 4.0})
}

func TestProxyResult_isReturnedAsSentWhenNothingIsTrimmed(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		args     map[string]any
	}{
		{
			name:     "raw projection skips the server's projection",
			upstream: `{"content":[{"type":"text","text":"[1,2,3,4]"}]}`,
			args:     map[string]any{"__mini": map[string]any{"projection": "raw"}},
		},
		{
			name:     "an isError result is not trimmed",
			upstream: `{"isError":true,"content":[{"type":"text","text":"[1,2,3,4]"}]}`,
			args:     map[string]any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, dir := newProxyServer(t, tc.upstream, arrayLimit)
			assertDeepEqual(t, any(callProxyTool(t, srv, tc.args)), decodeJSON(t, tc.upstream))
			assertNoFiles(t, dir)
		})
	}
}

func proxyStatsAfterCall(t *testing.T, srv *server.Server) (servers, sessions, result map[string]any) {
	t.Helper()
	const sessionID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	postMCP(t, srv, sessionID, initMsg(false))
	callResp := postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "svc__tool", "arguments": map[string]any{}},
	})
	resp := postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{"action": "status"}},
	})
	var status map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &status); err != nil {
		t.Fatal(err)
	}
	servers, _ = status["servers"].(map[string]any)["svc"].(map[string]any)
	sessions, _ = status["sessions"].(map[string]any)
	result, _ = callResp["result"].(map[string]any)
	return servers, sessions, result
}

func TestProxyResult_tokenStats(t *testing.T) {
	upstream := `{"content":[{"type":"text","text":"[` + strings.Repeat("0,", 299) + `0]"}]}`
	t.Run("a passthrough records the call and saves nothing", func(t *testing.T) {
		srv, _ := newProxyServer(t, upstream, nil)
		servers, sessions, _ := proxyStatsAfterCall(t, srv)
		if servers["est_tokens_saved"] != nil || sessions["est_tokens_saved"] != float64(0) ||
			sessions["calls"] != float64(1) {
			t.Errorf("servers=%v sessions=%v", servers, sessions)
		}
	})
	t.Run("a trimmed result saves upstream minus returned tokens", func(t *testing.T) {
		srv, _ := newProxyServer(t, upstream, arrayLimit)
		_, sessions, result := proxyStatsAfterCall(t, srv)
		wantSaved := len(upstream)/3 - len(jsonText(t, result))/3
		if sessions["est_tokens_saved"] != float64(wantSaved) || wantSaved <= 0 {
			t.Errorf("sessions=%v wantSaved=%d", sessions, wantSaved)
		}
	})
	t.Run("an isError result is recorded as a failed call", func(t *testing.T) {
		srv, _ := newProxyServer(t, `{"isError":true,"content":[{"type":"text","text":"boom"}]}`, nil)
		_, sessions, _ := proxyStatsAfterCall(t, srv)
		if sessions["errors"] != float64(1) || sessions["calls"] != float64(1) {
			t.Errorf("sessions=%v", sessions)
		}
	})
}

func assertNoFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no recovery files in %s, found %d", dir, len(entries))
	}
}

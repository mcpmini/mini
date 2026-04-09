//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// fakeHTTPMCP is an in-process HTTP MCP server for tests.
// onCall is invoked for every tools/call; returning status=0 falls through to default behavior.
type fakeHTTPMCP struct {
	srv    *httptest.Server
	calls  atomic.Int64
	onCall func(callNum int) (httpStatus int, body []byte)
}

func newFakeHTTPMCP(t *testing.T, onCall func(int) (int, []byte)) *fakeHTTPMCP {
	t.Helper()
	f := &fakeHTTPMCP{onCall: onCall}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.srv.Close)
	return f
}

func fakeMCPResult(method string) any {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fakehttpmcp", "version": "0.1.0"},
		}
	case "tools/list":
		return map[string]any{
			"tools": []map[string]any{{"name": "get_item", "description": "test tool",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}},
		}
	case "tools/call":
		return map[string]any{"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}
	default:
		return map[string]any{}
	}
}

func (f *fakeHTTPMCP) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	w.Header().Set("Content-Type", "application/json")
	if req.Method == "tools/call" && f.onCall != nil {
		if status, body := f.onCall(int(f.calls.Add(1))); status != 0 {
			w.WriteHeader(status)
			w.Write(body) //nolint:errcheck
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": fakeMCPResult(req.Method)}) //nolint:errcheck
}

func writeHTTPServerYAML(t *testing.T, configDir, serverName, url string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf("name: %s\ntransport: sse\nurl: %s\n", serverName, url)
	if err := os.WriteFile(filepath.Join(dir, serverName+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}

func httpServer(t *testing.T, onCall func(int) (int, []byte)) (*fakeHTTPMCP, *mcpClient) {
	t.Helper()
	f := newFakeHTTPMCP(t, onCall)
	cfg := t.TempDir()
	writeHTTPServerYAML(t, cfg, "svc", f.srv.URL)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	return f, startServer(t, cfg)
}

// envelopeOK returns true when call succeeds and the envelope ok field is true.
func envelopeOK(t *testing.T, client *mcpClient, server, tool string) bool {
	t.Helper()
	text, isRPCErr := client.execToolAllowError(server, tool, nil)
	if isRPCErr {
		return false
	}
	var e envelope
	if err := json.Unmarshal([]byte(text), &e); err != nil {
		return false
	}
	return e.Error == ""
}

// authCapturingMCP wraps a fakeHTTPMCP to capture a specific request header on each call.
func authCapturingMCP(t *testing.T, headerKey string) (*fakeHTTPMCP, *atomic.Value) {
	t.Helper()
	var captured atomic.Value
	f := &fakeHTTPMCP{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Store(r.Header.Get(headerKey))
		f.serveHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f, &captured
}

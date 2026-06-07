//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestHTTPServer_staleSessionFails proves the daemon-restart hang: if the proxy
// reuses a session ID from a previous daemon instance, the new daemon must return
// an error promptly (within seconds) rather than blocking indefinitely.
//
// Before the fix, waitInitialized used r.Context() (no deadline) and initAbort
// was never closed for the HTTP path, so this blocked forever.
func TestHTTPServer_staleSessionFails(t *testing.T) {
	_, ts1 := newHTTPTestServer(t)
	sessionID := initSession(t, ts1)
	ts1.Close() // "daemon restart"

	_, ts2 := newHTTPTestServer(t) // cleanup registered by newHTTPTestServer

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list", "arguments": map[string]any{}},
	})

	done := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, ts2.URL+"/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Mcp-Session-Id", sessionID)
		resp, err := ts2.Client().Do(req)
		if err == nil {
			done <- resp
		}
	}()

	select {
	case resp := <-done:
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var rpc map[string]any
		json.Unmarshal(b, &rpc) //nolint:errcheck
		if rpc["error"] == nil {
			t.Errorf("expected JSON-RPC error for stale session, got: %s", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stale session blocked indefinitely — daemon restart hang not fixed")
	}
}

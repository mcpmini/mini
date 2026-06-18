package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/version"
)

func okRPCResponse(id any) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"ok":true}}`, id))
}

func newHungServer(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(done); srv.Close() })
	return srv
}

func newJSONRPCServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func mustHTTPConn(t *testing.T, cfg HTTPConnectionConfig) *HTTPConnection {
	t.Helper()
	conn, err := NewHTTPConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func expectCallErrorContains(t *testing.T, conn *HTTPConnection, want string) {
	t.Helper()
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected call error")
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should mention %s, got: %v", want, err)
	}
}

func assertFastTimeout(t *testing.T, start time.Time) {
	t.Helper()
	if time.Since(start) > 5*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func mustListTools(t *testing.T, conn *HTTPConnection) []ToolDefinition {
	t.Helper()
	got, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return got
}

func TestHTTPConnection_versionHeaderSent(t *testing.T) {
	var receivedVersion string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("X-Mini-Version")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": map[string]any{},
		})
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	conn.Call(t.Context(), "ping", nil) //nolint:errcheck

	if receivedVersion != version.Version {
		t.Errorf("expected X-Mini-Version: %s, got %q", version.Version, receivedVersion)
	}
}

func TestHTTPConnection_versionHeaderAlwaysSent(t *testing.T) {
	var receivedVersion string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("X-Mini-Version")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": map[string]any{},
		})
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	conn.Call(t.Context(), "ping", nil) //nolint:errcheck

	if receivedVersion == "" {
		t.Error("expected X-Mini-Version header to be sent")
	}
}

func TestHTTPConnection_customHeadersSent(t *testing.T) {
	var gotAuth string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": map[string]any{},
		})
	})
	headers := map[string]string{"Authorization": "Bearer mytoken"}
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Headers: headers})
	conn.Call(t.Context(), "ping", nil) //nolint:errcheck

	if gotAuth != "Bearer mytoken" {
		t.Errorf("expected Authorization header, got %q", gotAuth)
	}
}

func TestHTTPConnection_sessionIDPersisted(t *testing.T) {
	callCount := 0
	var receivedSessionID string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Mcp-Session-Id", "sess-abc")
		} else {
			receivedSessionID = r.Header.Get("Mcp-Session-Id")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": callCount, "result": map[string]any{},
		})
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	conn.Call(t.Context(), "first", nil)  //nolint:errcheck
	conn.Call(t.Context(), "second", nil) //nolint:errcheck

	if receivedSessionID != "sess-abc" {
		t.Errorf("expected session ID sess-abc on second call, got %q", receivedSessionID)
	}
}

func TestHTTPConnection_4xxError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not authorized", http.StatusUnauthorized)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	expectCallErrorContains(t, conn, "401")
}

func TestHTTPConnection_5xxError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	expectCallErrorContains(t, conn, "500")
}

func TestHTTPConnection_502Error(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	expectCallErrorContains(t, conn, "502")
}

func TestHTTPConnection_200WithNonJSONBody(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not json</html>")) //nolint:errcheck
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for non-JSON 200 response")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error should mention parse failure, got: %v", err)
	}
}

func TestHTTPConnection_connectionDroppedMidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc"`)) //nolint:errcheck
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for connection dropped mid-response")
	}
}

func TestHTTPConnection_redirectBlocked(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect was followed — session token could be exfiltrated")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}})
	}))
	defer target.Close()

	redirecter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirecter.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: redirecter.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	_ = err
}

func TestHTTPClientTimeout_firesForHungServer(t *testing.T) {
	srv := newHungServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, ClientTimeout: 100 * time.Millisecond})
	start := time.Now()
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected timeout error for hung server")
	}
	assertFastTimeout(t, start)
}

func TestHTTPClientTimeout_defaultAllowsLongRunning(t *testing.T) {
	slow := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write(okRPCResponse(1))
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: slow.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err != nil {
		t.Fatalf("unexpected error for slow-but-valid server: %v", err)
	}
}

func TestHTTPClientTimeout_contextFiresBeforeClientTimeout(t *testing.T) {
	srv := newHungServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, ClientTimeout: 10 * time.Minute})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := conn.Call(ctx, "ping", nil)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	assertFastTimeout(t, start)
}

func newHandshakeServer(t *testing.T, toolsResult any, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if calls != nil {
			*calls++
		}
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": toolsResult},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListTools_successfulHandshakeAndList(t *testing.T) {
	var calls int
	tools := []any{map[string]any{"name": "do_thing", "description": "does a thing", "inputSchema": map[string]any{}}}
	srv := newHandshakeServer(t, tools, &calls)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	got := mustListTools(t, conn)
	if len(got) != 1 || got[0].Name != "do_thing" {
		t.Errorf("unexpected tools: %+v", got)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (initialize + notifications/initialized + tools/list), got %d", calls)
	}
}

func TestListTools_propagatesInputSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"path":{"type":"string"}}}`
	tools := []any{map[string]any{"name": "read_file", "inputSchema": json.RawMessage(schema)}}
	srv := newHandshakeServer(t, tools, nil)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	got := mustListTools(t, conn)
	if string(got[0].InputSchema) != schema {
		t.Errorf("schema mismatch: got %s", got[0].InputSchema)
	}
}

func TestHealth_returns200(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if err := conn.Health(context.Background()); err != nil {
		t.Errorf("expected healthy, got: %v", err)
	}
}

func TestHealth_returns500_isError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if err := conn.Health(context.Background()); err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestClose_isNoop(t *testing.T) {
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: "http://localhost:1"})
	if err := conn.Close(); err != nil {
		t.Errorf("Close() returned unexpected error: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestListTools_HTTPPagination(t *testing.T) {
	var page int
	pages := []map[string]any{
		{"tools": []any{map[string]any{"name": "a", "inputSchema": map[string]any{}}}, "nextCursor": "p2"},
		{"tools": []any{map[string]any{"name": "b", "inputSchema": map[string]any{}}}},
	}
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": pages[page],
			})
			page++
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	got, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("got %v, want %v", toolNames(got), want)
	}
}

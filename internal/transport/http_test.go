//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
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
	if cfg.Clock == nil {
		cfg.Clock = clock.NewFake()
	}
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

func TestHTTPConnection_buffersNotificationUntilHandlerInstalled(t *testing.T) {
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: "http://example.invalid"})
	for range 32 {
		conn.dispatchNotification(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
		conn.dispatchNotification(Notification{JSONRPC: "2.0", Method: NotificationToolsChanged})
	}

	received := make(chan Notification, 8)
	conn.SetNotificationHandler(func(notification Notification) { received <- notification })
	select {
	case notification := <-received:
		if notification.Method != NotificationToolsChanged {
			t.Fatalf("method = %q", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification emitted before handler installation was lost")
	}
	select {
	case notification := <-received:
		t.Fatalf("expected one coalesced pending invalidation, got %q", notification.Method)
	default:
	}

	conn.dispatchNotification(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
	conn.dispatchNotification(Notification{JSONRPC: "2.0", Method: NotificationToolsChanged})
	got := []string{
		mustReceiveBufferedNotification(t, received).Method,
		mustReceiveBufferedNotification(t, received).Method,
	}
	if !slices.Equal(got, []string{NotificationInitialized, NotificationToolsChanged}) {
		t.Fatalf("post-install notifications = %v", got)
	}
}

func mustReceiveBufferedNotification(t *testing.T, ch <-chan Notification) Notification {
	t.Helper()
	select {
	case notification := <-ch:
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
		return Notification{}
	}
}

func TestHTTPConnection_4xxError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not authorized", http.StatusUnauthorized)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	expectCallErrorContains(t, conn, "401")
}

func TestHTTPConnection_401WrapsUnauthorizedError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		http.Error(w, "not authorized", http.StatusUnauthorized)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected call error")
	}
	var uerr *UnauthorizedError
	if !errors.As(err, &uerr) {
		t.Fatalf("expected error to wrap *UnauthorizedError, got: %v", err)
	}
	want := `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`
	if uerr.WWWAuthenticate != want {
		t.Errorf("WWWAuthenticate = %q, want %q", uerr.WWWAuthenticate, want)
	}
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

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: redirecter.URL, Clock: clock.NewFake()})
	_, err := conn.Call(t.Context(), "ping", nil)
	_ = err
}

func assertFastTimeout(t *testing.T, start time.Time) {
	t.Helper()
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("timeout fired too slowly: %v", elapsed)
	}
}

func TestHTTPClientTimeout_firesForHungServer(t *testing.T) {
	srv := newHungServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, ClientTimeout: 100 * time.Millisecond})
	start := time.Now()
	_, err := conn.rpc(t.Context(), "ping", nil)
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
	_, err := conn.rpc(t.Context(), "ping", nil)
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
	_, err := conn.rpc(ctx, "ping", nil)
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

func TestListTools_deadlineErrorsWhenInitializeHangs(t *testing.T) {
	srv := newHungServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := conn.ListTools(ctx)
	if err == nil {
		t.Fatal("expected deadline error for hung initialize")
	}
	assertFastTimeout(t, start)
}

func TestListTools_retriesInitializeAfterFailure(t *testing.T) {
	var initializeCalls int
	var listCalls int
	tools := []any{map[string]any{"name": "do_thing", "inputSchema": map[string]any{}}}
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			initializeCalls++
			if initializeCalls == 1 {
				http.Error(w, "transient initialize failure", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "tools/list":
			listCalls++
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": tools},
			})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.ListTools(context.Background()); err == nil {
		t.Fatal("expected first ListTools call to fail")
	}
	got := mustListTools(t, conn)

	if want := []string{"do_thing"}; !slices.Equal(toolNames(got), want) {
		t.Fatalf("tool names = %v, want %v", toolNames(got), want)
	}
	if initializeCalls != 2 {
		t.Fatalf("initialize calls = %d, want 2", initializeCalls)
	}
	if listCalls != 1 {
		t.Fatalf("tools/list calls = %d, want 1", listCalls)
	}
}

func TestListTools_reusesSuccessfulInitialize(t *testing.T) {
	var initializeCalls int
	var listCalls int
	tools := []any{map[string]any{"name": "do_thing", "inputSchema": map[string]any{}}}
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			initializeCalls++
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "tools/list":
			listCalls++
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": tools},
			})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	mustListTools(t, conn)
	mustListTools(t, conn)

	if initializeCalls != 1 {
		t.Fatalf("initialize calls = %d, want 1", initializeCalls)
	}
	if listCalls != 2 {
		t.Fatalf("tools/list calls = %d, want 2", listCalls)
	}
}

func TestListTools_propagatesInputSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"path":{"type":"string"}}}`
	outputSchema := `{"type":"string"}`
	tools := []any{map[string]any{
		"name":         "read_file",
		"inputSchema":  json.RawMessage(schema),
		"title":        json.RawMessage(`"Read File"`),
		"outputSchema": json.RawMessage(outputSchema),
	}}
	srv := newHandshakeServer(t, tools, nil)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	got := mustListTools(t, conn)
	if string(got[0].InputSchema) != schema {
		t.Errorf("schema mismatch: got %s", got[0].InputSchema)
	}
	if len(got[0].Title) == 0 {
		t.Error("Title not propagated from upstream")
	}
	if string(got[0].OutputSchema) != outputSchema {
		t.Errorf("OutputSchema mismatch: got %s, want %s", got[0].OutputSchema, outputSchema)
	}
}

type staticAuthProvider struct{ value string }

func (s staticAuthProvider) Authorization(context.Context) (string, error) { return s.value, nil }
func (s staticAuthProvider) RefreshAuthorization(_ context.Context, _ string) (string, error) {
	return s.value, nil
}

func TestHTTPConnection_authProviderHeader(t *testing.T) {
	cases := []struct {
		name       string
		headerName string
		wantHeader string
	}{
		{"defaults to Authorization", "", "Authorization"},
		{"custom header name", "X-Custom-Auth", "X-Custom-Auth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(tc.wantHeader)
				w.Write(okRPCResponse(1)) //nolint:errcheck
			})
			conn := mustHTTPConn(t, HTTPConnectionConfig{
				URL: srv.URL, AuthProvider: staticAuthProvider{value: "Bearer dyn"}, AuthHeaderName: tc.headerName,
			})
			conn.Call(t.Context(), "ping", nil) //nolint:errcheck
			if got != "Bearer dyn" {
				t.Errorf("%s = %q, want %q", tc.wantHeader, got, "Bearer dyn")
			}
		})
	}
}

func TestHealth_usesAuthProvider(t *testing.T) {
	var got string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: staticAuthProvider{value: "Bearer dyn"}})
	if err := conn.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if got != "Bearer dyn" {
		t.Errorf("Health Authorization = %q, want %q", got, "Bearer dyn")
	}
}

type failingAuthProvider struct{ err error }

func (f failingAuthProvider) Authorization(context.Context) (string, error) { return "", f.err }
func (f failingAuthProvider) RefreshAuthorization(_ context.Context, _ string) (string, error) {
	return "", f.err
}

func TestHTTPConnection_authProviderErrorNamesRemedy(t *testing.T) {
	var calls int
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL: srv.URL, ServerName: "myserver",
		AuthProvider: failingAuthProvider{err: fmt.Errorf("myserver requires re-authorization; run `mini auth myserver`: no token")},
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "mini auth myserver") {
		t.Errorf("expected remedy error, got: %v", err)
	}
	if calls != 0 {
		t.Errorf("no request should reach upstream when auth cannot be built, got %d", calls)
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
	cursorCh := make(chan string, 2)
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
			var cursor string
			if params, ok := req["params"].(map[string]any); ok {
				cursor, _ = params["cursor"].(string)
			}
			cursorCh <- cursor
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
	gotCursors := []string{<-cursorCh, <-cursorCh}
	if want := []string{"", "p2"}; !slices.Equal(gotCursors, want) {
		t.Errorf("cursors: got %v, want %v", gotCursors, want)
	}
}

// strictSessionServer rejects any non-initialize request that doesn't carry
// the session ID it issued at initialize, mirroring Atlassian's MCP server
// ("Request must be an initialize request if no session ID is provided.").
type strictSessionServer struct {
	sessionID string
	mu        sync.Mutex
	methodLog []string
	initCount int
}

func newStrictSessionServer(t *testing.T) (*httptest.Server, *strictSessionServer) {
	t.Helper()
	s := &strictSessionServer{sessionID: "sess-strict-1"}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	return srv, s
}

func (s *strictSessionServer) handle(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	method, _ := req["method"].(string)

	s.mu.Lock()
	s.methodLog = append(s.methodLog, method)
	s.mu.Unlock()

	if method != "initialize" && r.Header.Get("Mcp-Session-Id") != s.sessionID {
		http.Error(w, `{"error":"Request must be an initialize request if no session ID is provided."}`, http.StatusBadRequest)
		return
	}
	s.respond(w, method, req)
}

func (s *strictSessionServer) respond(w http.ResponseWriter, method string, req map[string]any) {
	switch method {
	case "initialize":
		s.mu.Lock()
		s.initCount++
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", s.sessionID)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"protocolVersion": ProtocolVersion},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"tools": []any{}},
		})
	case "tools/call":
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"content": []any{}},
		})
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
	}
}

func TestHTTPConnection_callCompletesHandshakeBeforeToolCall(t *testing.T) {
	srv, fake := newStrictSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	args, _ := json.Marshal(ToolCallParams{Name: "do_thing", Arguments: map[string]any{}})

	if _, err := conn.Call(t.Context(), "tools/call", args); err != nil {
		t.Fatalf("Call against strict session server failed: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	want := []string{"initialize", "notifications/initialized", "tools/call"}
	if !slices.Equal(fake.methodLog, want) {
		t.Fatalf("method order = %v, want %v", fake.methodLog, want)
	}
}

func TestHTTPConnection_concurrentFirstCallsHandshakeOnce(t *testing.T) {
	srv, fake := newStrictSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = conn.Call(context.Background(), "tools/list", nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d failed: %v", i, err)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.initCount != 1 {
		t.Errorf("initCount = %d, want 1", fake.initCount)
	}
}

func TestHTTPConnection_callRetriesHandshakeAfterFailure(t *testing.T) {
	var initializeCalls int
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			initializeCalls++
			if initializeCalls == 1 {
				http.Error(w, "transient initialize failure", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "ping":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"ok": true},
			})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("expected first Call to fail when handshake fails")
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected retried handshake to succeed, got: %v", err)
	}
	if initializeCalls != 2 {
		t.Fatalf("initialize calls = %d, want 2", initializeCalls)
	}
}

// sync.Mutex is not ctx-aware: a cancelled caller cannot abort a peer's in-progress
// handshake; if it wins the lock first, its attempt fails on its cancelled ctx and
// leaves initialized==false for the next caller to retry.
func TestHTTPConnection_cancelledConcurrentCallerDoesNotPoisonHandshake(t *testing.T) {
	srv, fake := newStrictSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	var cancelledErr, survivorErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, cancelledErr = conn.Call(cancelledCtx, "tools/list", nil)
	}()
	go func() {
		defer wg.Done()
		_, survivorErr = conn.Call(context.Background(), "tools/list", nil)
	}()
	wg.Wait()

	if cancelledErr == nil {
		t.Error("expected the cancelled-ctx caller to fail")
	}
	if survivorErr != nil {
		t.Errorf("expected the uncancelled caller to succeed, got: %v", survivorErr)
	}
	fake.mu.Lock()
	initCount := fake.initCount
	fake.mu.Unlock()
	if initCount != 1 {
		t.Errorf("initCount = %d, want exactly 1 successful handshake", initCount)
	}
	if _, err := conn.Call(context.Background(), "tools/list", nil); err != nil {
		t.Errorf("connection left unusable after cancellation race: %v", err)
	}
}

func TestHTTPConnection_listToolsThenCallSharesOneHandshake(t *testing.T) {
	srv, fake := newStrictSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	args, _ := json.Marshal(ToolCallParams{Name: "do_thing", Arguments: map[string]any{}})
	if _, err := conn.Call(context.Background(), "tools/call", args); err != nil {
		t.Fatalf("Call: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.initCount != 1 {
		t.Errorf("initCount = %d, want 1", fake.initCount)
	}
}

func TestHealth_doesNotTriggerHandshake(t *testing.T) {
	srv, fake := newStrictSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if err := conn.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.initCount != 0 {
		t.Errorf("initCount = %d, want 0 (Health must not trigger a handshake)", fake.initCount)
	}
}

func newHandshakeServerWithNotif(t *testing.T, notifHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "notifications/initialized":
			notifHandler(w, r)
		case "ping":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"ok": true},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandshake_initializedNotification401_handshakeFails_and_isRetryable(t *testing.T) {
	var notifCalls atomic.Int32
	srv := newHandshakeServerWithNotif(t, func(w http.ResponseWriter, r *http.Request) {
		if notifCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected handshake failure when notification returns 401")
	}
	if !strings.Contains(err.Error(), "notifications/initialized") {
		t.Errorf("error should mention notifications/initialized, got: %v", err)
	}

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Errorf("expected handshake retry to succeed, got: %v", err)
	}
}

func TestHandshake_initializedNotification500_handshakeFails(t *testing.T) {
	srv := newHandshakeServerWithNotif(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for notification 500")
	}
	if !strings.Contains(err.Error(), "notifications/initialized") {
		t.Errorf("error should mention notifications/initialized, got: %v", err)
	}
}

func TestHandshake_initializedNotification401_withProvider_refreshAndSucceeds(t *testing.T) {
	var notifCalls atomic.Int32
	srv := newHandshakeServerWithNotif(t, func(w http.ResponseWriter, r *http.Request) {
		notifCalls.Add(1)
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	})
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: provider})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after auth refresh, got: %v", err)
	}
	if notifCalls.Load() != 2 {
		t.Errorf("notification calls = %d, want 2 (401 then replay with new token)", notifCalls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want 1", provider.refreshCount())
	}
}

func TestNotificationStream_401_refreshesAndReconnects(t *testing.T) {
	streamConnected := make(chan struct{})
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.Header.Get("Authorization") != "Bearer new" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"%s\"}\n\n", NotificationToolsChanged) //nolint:errcheck
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(streamConnected)
			<-r.Context().Done()
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{
					"protocolVersion": ProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "ping":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"ok": true},
			})
		}
	}))
	t.Cleanup(srv.Close)

	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: provider})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}

	select {
	case <-streamConnected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream to connect with refreshed token")
	}

	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want 1", provider.refreshCount())
	}
	if got := provider.recordedStale(); got != "Bearer old" {
		t.Errorf("stale passed to RefreshAuthorization = %q, want %q", got, "Bearer old")
	}
}

// refreshSucceedsProvider succeeds on Authorization and RefreshAuthorization so that
// postWithAuthRetry can replay the request; the server still returns 401 on the replay,
// which should produce an error wrapping ErrReauthRequired.
type refreshSucceedsProvider struct{}

func (refreshSucceedsProvider) Authorization(_ context.Context) (string, error) {
	return "Bearer initial", nil
}
func (refreshSucceedsProvider) RefreshAuthorization(_ context.Context, _ string) (string, error) {
	return "Bearer refreshed", nil
}

func TestPostWithAuthRetry_replay401WrapsErrReauthRequired(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL: srv.URL, ServerName: "svc", AuthProvider: refreshSucceedsProvider{},
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("expected ErrReauthRequired after replay 401, got: %v", err)
	}
}

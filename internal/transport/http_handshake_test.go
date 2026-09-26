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
)

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
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: provider, AuthHeaderName: "Authorization"})

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

	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: provider, AuthHeaderName: "Authorization"})

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
	if got := provider.staleValue(); got != "Bearer old" {
		t.Errorf("stale passed to RefreshAuthorization = %q, want %q", got, "Bearer old")
	}
}

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
		URL: srv.URL, ServerName: "svc", AuthProvider: refreshSucceedsProvider{}, AuthHeaderName: "Authorization",
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("expected ErrReauthRequired after replay 401, got: %v", err)
	}
}

type strictInitServer struct {
	mu             sync.Mutex
	notifCallCount int
	initSessionIDs []string
}

func newStrictInitServer(t *testing.T) (*httptest.Server, *strictInitServer) {
	t.Helper()
	s := &strictInitServer{}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	return srv, s
}

func (s *strictInitServer) handle(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	switch req["method"] {
	case "initialize":
		s.mu.Lock()
		s.initSessionIDs = append(s.initSessionIDs, r.Header.Get("Mcp-Session-Id"))
		n := len(s.initSessionIDs)
		s.mu.Unlock()
		if r.Header.Get("Mcp-Session-Id") != "" {
			http.Error(w, "initialize with session ID rejected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("sess-%d", n))
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"protocolVersion": ProtocolVersion},
		})
	case "notifications/initialized":
		s.mu.Lock()
		s.notifCallCount++
		n := s.notifCallCount
		s.mu.Unlock()
		if n == 1 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	case "ping":
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"ok": true},
		})
	}
}

func TestHandshake_initializedNotificationBothUnauthorized_isReauthRequired(t *testing.T) {
	srv := newHandshakeServerWithNotif(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL: srv.URL, AuthProvider: provider, AuthHeaderName: "Authorization",
	})

	_, err := conn.Call(t.Context(), "ping", nil)
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("expected ErrReauthRequired when notifications/initialized always 401, got: %v", err)
	}
}

func TestHandshake_sessionIDClearedAfterNotificationFailure(t *testing.T) {
	srv, fake := newStrictInitServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("expected first Call to fail when notifications/initialized returns 503")
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected second Call to succeed after session ID cleared, got: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.initSessionIDs) < 2 {
		t.Fatalf("expected 2 initialize calls, got %d", len(fake.initSessionIDs))
	}
	if fake.initSessionIDs[1] != "" {
		t.Errorf("second initialize carried stale session ID %q, want empty", fake.initSessionIDs[1])
	}
}

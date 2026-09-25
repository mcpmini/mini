//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
)

type fakeAuthProvider struct {
	mu         sync.Mutex
	current    string
	next       string
	refreshes  int
	authErr    error
	refreshErr error
	authValues []string
	lastStale  string
	authCalls  int
}

func (f *fakeAuthProvider) Authorization(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls++
	if f.authErr != nil {
		return "", f.authErr
	}
	if len(f.authValues) > 0 {
		value := f.authValues[0]
		f.authValues = f.authValues[1:]
		return value, nil
	}
	return f.current, nil
}

func (f *fakeAuthProvider) RefreshAuthorization(_ context.Context, stale string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastStale = stale
	if f.current != stale {
		return f.current, nil
	}
	f.refreshes++
	if f.refreshErr != nil {
		return "", f.refreshErr
	}
	f.current = f.next
	return f.current, nil
}

func (f *fakeAuthProvider) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

func (f *fakeAuthProvider) staleValue() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastStale
}

func (f *fakeAuthProvider) authorizationCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authCalls
}

func newAuthReplayConn(t *testing.T, handler http.HandlerFunc) (*HTTPConnection, *fakeAuthProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: srv.URL, Clock: clock.NewFake(), ServerName: "myserver",
		AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, provider
}

func TestNewHTTPConnection_authProviderWithoutHeaderName_isError(t *testing.T) {
	_, err := NewHTTPConnection(HTTPConnectionConfig{
		URL:          "http://localhost:1",
		Clock:        clock.NewFake(),
		AuthProvider: &fakeAuthProvider{current: "Bearer x"},
	})
	if err == nil {
		t.Fatal("expected error when AuthProvider set without AuthHeaderName")
	}
}

func TestCall_authProvider_setsConfiguredHeader(t *testing.T) {
	var got string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Custom-Auth")
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL:            srv.URL,
		AuthProvider:   &fakeAuthProvider{current: "Bearer dyn"},
		AuthHeaderName: "X-Custom-Auth",
	})
	conn.Call(t.Context(), "ping", nil) //nolint:errcheck
	if got != "Bearer dyn" {
		t.Errorf("X-Custom-Auth = %q, want %q", got, "Bearer dyn")
	}
}

func TestHealth_authProvider_sendsProviderValue(t *testing.T) {
	var gotAuth, gotContentType, gotAccept, gotSession string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		gotSession = r.Header.Get("Mcp-Session-Id")
		w.WriteHeader(http.StatusOK)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL:            srv.URL,
		AuthProvider:   &fakeAuthProvider{current: "Bearer dyn"},
		AuthHeaderName: "Authorization",
	})
	if err := conn.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotAuth != "Bearer dyn" {
		t.Errorf("Health Authorization = %q, want %q", gotAuth, "Bearer dyn")
	}
	if gotContentType != "" {
		t.Errorf("Health must not send Content-Type, got %q", gotContentType)
	}
	if strings.Contains(gotAccept, "text/event-stream") {
		t.Errorf("Health must not send Accept: text/event-stream, got %q", gotAccept)
	}
	if gotSession != "" {
		t.Errorf("Health must not send Mcp-Session-Id, got %q", gotSession)
	}
}

func TestCall_authProviderError_failsWithoutContactingUpstream(t *testing.T) {
	var calls int
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL:            srv.URL,
		ServerName:     "myserver",
		AuthProvider:   &fakeAuthProvider{authErr: errors.New("no token")},
		AuthHeaderName: "Authorization",
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("expected provider error to propagate, got: %v", err)
	}
	if calls != 0 {
		t.Errorf("no request should reach upstream when auth cannot be built, got %d", calls)
	}
}

func TestCall_401_refreshesAndReplaysOnce(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after 401 refresh replay, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("upstream attempts = %d, want 2", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want 1", provider.refreshCount())
	}
}

func TestCall_401_refreshesWithTheValueActuallySent(t *testing.T) {
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	provider.current = "Bearer new"
	provider.authValues = []string{"Bearer old-sent"}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after replay, got: %v", err)
	}
	if got := provider.staleValue(); got != "Bearer old-sent" {
		t.Errorf("stale = %q, want the value actually sent in the first request", got)
	}
	if got := provider.authorizationCalls(); got != 2 {
		t.Errorf("Authorization calls = %d, want 2 (one per request)", got)
	}
}

func TestCall_401AfterReplay_returnsReauthError(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected terminal error")
	}
	if !strings.Contains(err.Error(), "myserver requires re-authorization") || !strings.Contains(err.Error(), "mini auth myserver") {
		t.Errorf("terminal error should name server and remedy, got: %v", err)
	}
	var uerr *UnauthorizedError
	if !errors.As(err, &uerr) {
		t.Errorf("terminal error must unwrap to *UnauthorizedError, got: %T %v", err, err)
	}
	if calls.Load() != 2 {
		t.Errorf("upstream attempts = %d, want exactly 2", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want exactly 1", provider.refreshCount())
	}
}

func TestCall_refreshFails_returnsErrorWithoutReplay(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	provider.refreshErr = errors.New("token endpoint down")
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "token endpoint down") {
		t.Errorf("error should contain cause, got: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("upstream attempts = %d, want 1 (no replay after failed refresh)", calls.Load())
	}
}

func TestCall_429Then401_retryBudgetNotMultiplied(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if n < maxRetries {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		} else if n == maxRetries {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error when second post exhausts 429 budget")
	}
	want := 2 * maxRetries
	if int(calls.Load()) != want {
		t.Errorf("upstream attempts = %d, want %d (2*maxRetries)", calls.Load(), want)
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want exactly 1", provider.refreshCount())
	}
}

func TestNotificationRequests_authProvider_sendHeader(t *testing.T) {
	provider := &fakeAuthProvider{current: "Bearer notif-token"}
	var initializedAuth, streamAuth string
	getReceived := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			streamAuth = r.Header.Get("Authorization")
			close(getReceived)
			w.WriteHeader(http.StatusMethodNotAllowed)
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
		case NotificationInitialized:
			initializedAuth = r.Header.Get("Authorization")
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": []any{}},
			})
		}
	}))
	t.Cleanup(srv.Close)

	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL:            srv.URL,
		Clock:          clock.NewFake(),
		AuthProvider:   provider,
		ServerName:     "testserver",
		AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	conn.ListTools(t.Context()) //nolint:errcheck
	select {
	case <-getReceived:
	case <-t.Context().Done():
		t.Fatal("notification stream GET never received")
	}

	if initializedAuth != "Bearer notif-token" {
		t.Errorf("notifications/initialized Authorization = %q, want %q", initializedAuth, "Bearer notif-token")
	}
	if streamAuth != "Bearer notif-token" {
		t.Errorf("notification stream GET Authorization = %q, want %q", streamAuth, "Bearer notif-token")
	}
}

func TestCall_staticAndProviderHeader_providerValueWins(t *testing.T) {
	var got string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL:            srv.URL,
		Headers:        map[string]string{"Authorization": "Bearer static"},
		AuthProvider:   &fakeAuthProvider{current: "Bearer dyn"},
		AuthHeaderName: "Authorization",
	})
	conn.Call(t.Context(), "ping", nil) //nolint:errcheck
	if got != "Bearer dyn" {
		t.Errorf("Authorization = %q, want provider value %q", got, "Bearer dyn")
	}
}

func TestCall_replayFailsWithServerError_notReportedAsReauth(t *testing.T) {
	var calls atomic.Int32
	conn, _ := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "re-authorization") {
		t.Errorf("500 replay error must not report re-authorization, got: %v", err)
	}
}

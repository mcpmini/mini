//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type capturedRequest struct {
	Request
	Headers http.Header
}

func receiveRequest(t *testing.T, requests <-chan capturedRequest) capturedRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not receive the expected request")
		return capturedRequest{}
	}
}

func TestHTTPLegacyCancellationPreservesRequestIdentity(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(fmt.Sprint("response_started=", flush), func(t *testing.T) { checkHTTPLegacyCancellation(t, flush) })
	}
}

func checkHTTPLegacyCancellation(t *testing.T, flush bool) {
	requests := make(chan capturedRequest, 8)
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method == "tools/call" && flush {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		requests <- capturedRequest{Request: req, Headers: r.Header.Clone()}
		if req.Method == "tools/call" {
			<-r.Context().Done()
			return
		}
		if req.Method == NotificationCancelled {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(okRPCResponse(req.ID))
	})
	provider := &fakeAuthProvider{current: "Bearer original", next: "Bearer refreshed"}
	conn := mustLegacyHTTPConn(
		t,
		HTTPConnectionConfig{
			URL:            srv.URL,
			Clock:          clock.System(),
			AuthProvider:   provider,
			AuthHeaderName: "Authorization",
			Headers:        map[string]string{"X-Custom": "kept"},
		},
	)
	conn.initialized = true
	conn.storeSessionID("original-session")
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := conn.Call(ctx, "tools/call", nil); result <- err }()
	call := receiveRequest(t, requests)
	conn.storeSessionID("replacement-session")
	provider.setCurrentAuth("Bearer replacement")
	cancel()
	notice := receiveRequest(t, requests)
	assertHTTPCancellation(t, call, notice)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v", err)
	}
	if provider.refreshCount() != 0 || provider.authorizationCalls() != 1 {
		t.Fatal("cancellation entered authentication machinery")
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	if next := receiveRequest(t, requests); next.Method != "ping" {
		t.Fatalf("unexpected retry: %s", next.Method)
	}
}

func assertHTTPCancellation(t *testing.T, call, notice capturedRequest) {
	t.Helper()
	var params CancelledParams
	if notice.Method != NotificationCancelled || notice.ID != nil || json.Unmarshal(notice.Params, &params) != nil ||
		!sameJSONID(params.RequestID, call.ID) {
		t.Fatalf("notice %+v does not cancel call %v", notice.Request, call.ID)
	}
	for _, key := range []string{"Authorization", "Mcp-Session-Id", "Mcp-Protocol-Version", "X-Custom"} {
		if got, want := notice.Headers.Get(key), call.Headers.Get(key); got == "" || got != want {
			t.Errorf("notice %s = %q, want %q", key, got, want)
		}
	}
}

func TestHTTPCancellationSkipsHandshakeAndModernRequests(t *testing.T) {
	for _, method := range []string{"initialize", "server/discover", "tools/call"} {
		t.Run(method, func(t *testing.T) { checkHTTPNoCancellation(t, method) })
	}
}

func checkHTTPNoCancellation(t *testing.T, method string) {
	requests := make(chan capturedRequest, 8)
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests <- capturedRequest{Request: req}
		if req.Method == method {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(okRPCResponse(req.ID))
	})
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.System()})
	ctx, cancel := context.WithCancel(t.Context())
	call := httpCall{Request: Request{Method: method}}
	if method == "tools/call" {
		call.modernVersion = ModernVersions[0]
	}
	result := make(chan error, 1)
	go func() { _, err := conn.send(ctx, call); result <- err }()
	_ = receiveRequest(t, requests)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("send error = %v", err)
	}
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	if next := receiveRequest(t, requests); next.Method != "ping" {
		t.Fatalf("unexpected cancellation: %s", next.Method)
	}
}

func TestHTTPCancellationNoticeIsBounded(t *testing.T) {
	requests := make(chan capturedRequest, 8)
	srv := newJsonRPCServer(t, func(_ http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests <- capturedRequest{Request: req}
		<-r.Context().Done()
	})
	fakeClock := clock.NewFake()
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: fakeClock})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := conn.rpc(ctx, "tools/call", nil); result <- err }()
	_ = receiveRequest(t, requests)
	cancel()
	_ = receiveRequest(t, requests)
	fakeClock.Advance(upstreamCancelTimeout)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("rpc error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation waited beyond its delivery deadline")
	}
}

func TestHTTPCancellationDoesNotObserveNoticeChallenge(t *testing.T) {
	requests := make(chan capturedRequest, 4)
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests <- capturedRequest{Request: req}
		if req.Method == NotificationCancelled {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		<-r.Context().Done()
	})
	provider := &observingAuthProvider{}
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{
		URL: srv.URL, Clock: clock.System(), AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := conn.send(ctx, httpCall{Request: Request{Method: "tools/call"}})
		result <- err
	}()
	if req := receiveRequest(t, requests); req.Method != "tools/call" {
		t.Fatalf("initial request = %q", req.Method)
	}
	cancel()
	if req := receiveRequest(t, requests); req.Method != NotificationCancelled {
		t.Fatalf("request after cancellation = %q", req.Method)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("send error = %v", err)
	}
	if len(provider.observed) != 0 {
		t.Fatalf("cancellation challenge was observed: %#v", provider.observed)
	}
}

func TestHTTPCancellationSkipsUnsentAndCompletedAttempts(t *testing.T) {
	for _, outcome := range []string{"pre-cancelled", "success", "rpc-error", "backoff"} {
		t.Run(outcome, func(t *testing.T) { checkHTTPCompletedAttempt(t, outcome) })
	}
}

func checkHTTPCompletedAttempt(t *testing.T, outcome string) {
	requests := make(chan capturedRequest, 8)
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests <- capturedRequest{Request: req}
		if req.Method == "tools/call" && outcome == "backoff" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if req.Method == "tools/call" && outcome == "rpc-error" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"error":{"code":-32603,"message":"failed"}}`, req.ID)
			return
		}
		_, _ = w.Write(okRPCResponse(req.ID))
	})
	fakeClock := clock.NewFake()
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: fakeClock})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if outcome == "pre-cancelled" {
		cancel()
	}
	result := make(chan error, 1)
	go func() { _, err := conn.rpc(ctx, "tools/call", nil); result <- err }()
	if outcome != "pre-cancelled" {
		_ = receiveRequest(t, requests)
	}
	if outcome == "backoff" {
		wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
		defer stop()
		if err := fakeClock.BlockUntilContext(wait, 1); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	err := <-result
	if (outcome == "success") != (err == nil) {
		t.Fatalf("%s error = %v", outcome, err)
	}
	cancel()
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	if next := receiveRequest(t, requests); next.Method != "ping" {
		t.Fatalf("unexpected cancellation: %s", next.Method)
	}
}

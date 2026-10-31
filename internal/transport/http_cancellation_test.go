//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type capturedRequest struct {
	Request
	Headers http.Header
}

type upstreamReply func(w http.ResponseWriter, r *http.Request, req Request) (handled bool)

type upstreamBehavior struct {
	hold                string
	startHeldResponse   bool
	replyBeforeDefaults upstreamReply
}

type recordingUpstream struct {
	URL      string
	requests chan capturedRequest
	behavior upstreamBehavior
}

func newRecordingUpstream(t *testing.T, behavior upstreamBehavior) *recordingUpstream {
	up := &recordingUpstream{requests: make(chan capturedRequest, 8), behavior: behavior}
	up.URL = newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		up.serve(w, r, req)
	}).URL
	return up
}

func (up *recordingUpstream) serve(w http.ResponseWriter, r *http.Request, req Request) {
	held := req.Method == up.behavior.hold
	if held && up.behavior.startHeldResponse {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
	}
	up.requests <- capturedRequest{Request: req, Headers: r.Header.Clone()}
	reply := up.behavior.replyBeforeDefaults
	switch {
	case held:
		<-r.Context().Done()
	case reply != nil && reply(w, r, req):
	default:
		_, _ = w.Write(okRPCResponse(req.ID))
	}
}

func (up *recordingUpstream) expectNext(t *testing.T, method string) capturedRequest {
	t.Helper()
	select {
	case req := <-up.requests:
		if req.Method != method {
			t.Fatalf("upstream received %q, want %q", req.Method, method)
		}
		return req
	case <-time.After(3 * time.Second):
		t.Fatalf("upstream did not receive %q", method)
		return capturedRequest{}
	}
}

func (up *recordingUpstream) assertNoOtherRequest(t *testing.T, conn *HTTPConnection) {
	t.Helper()
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	up.expectNext(t, "ping")
}

func rejectNotice(w http.ResponseWriter, _ *http.Request, req Request) bool {
	if req.Method != NotificationCancelled {
		return false
	}
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	w.WriteHeader(http.StatusUnauthorized)
	return true
}

func holdNotice(_ http.ResponseWriter, r *http.Request, req Request) bool {
	if req.Method != NotificationCancelled {
		return false
	}
	<-r.Context().Done()
	return true
}

type inFlightCall struct {
	cancel context.CancelFunc
	result chan error
}

func startToolCall(t *testing.T, conn *HTTPConnection, call httpCall) *inFlightCall {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	inFlight := &inFlightCall{cancel: cancel, result: make(chan error, 1)}
	go func() { _, err := conn.send(ctx, call); inFlight.result <- err }()
	return inFlight
}

func (c *inFlightCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("call did not return")
		return nil
	}
}

func assertCanceled(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

var legacyToolCall = httpCall{Request: Request{Method: "tools/call"}}

func TestHTTPLegacyCancellationPreservesRequestIdentity(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint("response_started=", started), func(t *testing.T) { checkHTTPLegacyCancellation(t, started) })
	}
}

func checkHTTPLegacyCancellation(t *testing.T, responseStarted bool) {
	up := newRecordingUpstream(t, upstreamBehavior{
		hold: "tools/call", startHeldResponse: responseStarted, replyBeforeDefaults: rejectNotice,
	})
	provider := &observingAuthProvider{
		fakeAuthProvider: fakeAuthProvider{current: "Bearer original", next: "Bearer refreshed"},
	}
	conn := newSessionHTTPConn(t, up.URL, provider)
	call := startToolCall(t, conn, legacyToolCall)
	sent := up.expectNext(t, "tools/call")
	conn.storeSessionID("replacement-session")
	provider.setCurrentAuth("Bearer replacement")
	call.cancel()
	assertCancels(t, up.expectNext(t, NotificationCancelled), sent)
	assertCanceled(t, call.wait(t))
	if provider.refreshCount() != 0 || provider.authorizationCalls() != 1 || len(provider.observed) != 0 {
		t.Fatal("cancellation entered authentication machinery")
	}
	up.assertNoOtherRequest(t, conn)
}

func newSessionHTTPConn(t *testing.T, url string, provider AuthorizationProvider) *HTTPConnection {
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{
		URL: url, Clock: clock.System(), AuthProvider: provider, AuthHeaderName: "Authorization",
		Headers: map[string]string{"X-Custom": "kept"},
	})
	conn.initialized = true
	conn.storeSessionID("original-session")
	return conn
}

func assertCancels(t *testing.T, notice, call capturedRequest) {
	t.Helper()
	var params CancelledParams
	if notice.ID != nil || json.Unmarshal(notice.Params, &params) != nil || !sameJSONID(params.RequestID, call.ID) {
		t.Fatalf("notice %+v does not cancel call %v", notice.Request, call.ID)
	}
	for _, key := range []string{"Authorization", "Mcp-Session-Id", "Mcp-Protocol-Version", "X-Custom"} {
		if got, want := notice.Headers.Get(key), call.Headers.Get(key); got == "" || got != want {
			t.Errorf("notice %s = %q, want %q", key, got, want)
		}
	}
}

func TestHTTPCancellationSkipsHandshakeAndModernRequests(t *testing.T) {
	modernToolCall := httpCall{Request: Request{Method: "tools/call"}, modernVersion: ModernVersions[0]}
	for _, call := range []httpCall{
		{Request: Request{Method: "initialize"}},
		{Request: Request{Method: "server/discover"}},
		modernToolCall,
	} {
		t.Run(call.Method+call.modernVersion, func(t *testing.T) {
			up := newRecordingUpstream(t, upstreamBehavior{hold: call.Method})
			conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: up.URL, Clock: clock.System()})
			inFlight := startToolCall(t, conn, call)
			up.expectNext(t, call.Method)
			inFlight.cancel()
			assertCanceled(t, inFlight.wait(t))
			up.assertNoOtherRequest(t, conn)
		})
	}
}

func TestHTTPCancellationNoticeIsBounded(t *testing.T) {
	up := newRecordingUpstream(t, upstreamBehavior{hold: "tools/call", replyBeforeDefaults: holdNotice})
	fakeClock := clock.NewFake()
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: up.URL, Clock: fakeClock})
	call := startToolCall(t, conn, legacyToolCall)
	up.expectNext(t, "tools/call")
	call.cancel()
	up.expectNext(t, NotificationCancelled)
	fakeClock.Advance(upstreamCancelTimeout)
	assertCanceled(t, call.wait(t))
}

func TestHTTPCancellationSkipsPreCancelledCall(t *testing.T) {
	up := newRecordingUpstream(t, upstreamBehavior{})
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: up.URL, Clock: clock.System()})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := conn.send(ctx, legacyToolCall)
	assertCanceled(t, err)
	up.assertNoOtherRequest(t, conn)
}

func TestHTTPCancellationSkipsCompletedAttempts(t *testing.T) {
	for _, outcome := range []string{"success", "rpc-error", "backoff"} {
		t.Run(outcome, func(t *testing.T) { checkHTTPCompletedAttempt(t, outcome) })
	}
}

func checkHTTPCompletedAttempt(t *testing.T, outcome string) {
	up := newRecordingUpstream(t, upstreamBehavior{replyBeforeDefaults: completedToolCallReply(outcome)})
	fakeClock := clock.NewFake()
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: up.URL, Clock: fakeClock})
	call := startToolCall(t, conn, legacyToolCall)
	up.expectNext(t, "tools/call")
	if outcome == "backoff" {
		awaitRetryTimer(t, fakeClock)
		call.cancel()
	}
	if err := call.wait(t); (outcome == "success") != (err == nil) {
		t.Fatalf("%s error = %v", outcome, err)
	}
	call.cancel()
	up.assertNoOtherRequest(t, conn)
}

func completedToolCallReply(outcome string) upstreamReply {
	return func(w http.ResponseWriter, _ *http.Request, req Request) bool {
		switch {
		case req.Method != "tools/call" || outcome == "success":
			return false
		case outcome == "backoff":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"error":{"code":-32603,"message":"failed"}}`, req.ID)
		}
		return true
	}
}

func awaitRetryTimer(t *testing.T, fakeClock *clock.Fake) {
	t.Helper()
	wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if err := fakeClock.BlockUntilContext(wait, 1); err != nil {
		t.Fatal(err)
	}
}

type cancelBeforeWriteTransport struct {
	cancel  context.CancelFunc
	methods []string
}

func (f *cancelBeforeWriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body Request
	_ = json.NewDecoder(req.Body).Decode(&body)
	f.methods = append(f.methods, body.Method)
	f.cancel()
	return nil, req.Context().Err()
}

func TestHTTPCancellationSkipsAttemptCancelledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := &cancelBeforeWriteTransport{cancel: cancel}
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{
		URL: "http://upstream.invalid/mcp", Clock: clock.System(), Client: &http.Client{Transport: transport},
	})
	_, err := conn.send(ctx, legacyToolCall)
	assertCanceled(t, err)
	if len(transport.methods) != 1 {
		t.Fatalf("requests = %q, want only the unsent tools/call", transport.methods)
	}
}

func TestHTTPAbandonedWhileWaiting(t *testing.T) {
	live, cancelled := t.Context(), cancelledContext(t)
	clientTimeout := &url.Error{Op: "Post", URL: "/", Err: &net.DNSError{IsTimeout: true}}
	reset := &url.Error{Op: "Post", URL: "/", Err: errors.New("connection reset")}
	for _, tt := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "caller cancelled", ctx: cancelled, err: fmt.Errorf("read response: %w", context.Canceled), want: true},
		{name: "client timeout, as tool_timeout 0 leaves it", ctx: live, err: clientTimeout, want: true},
		{name: "client timeout reading body", ctx: live, err: fmt.Errorf("read response: %w", clientTimeout), want: true},
		{name: "connection reset", ctx: live, err: reset, want: false},
		{name: "rpc error after caller left", ctx: cancelled, err: &RPCError{Code: -32603}, want: false},
		{name: "completed", ctx: live, err: nil, want: false},
	} {
		if got := abandonedWhileWaiting(tt.ctx, tt.err); got != tt.want {
			t.Errorf("%s: abandonedWhileWaiting = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func cancelledContext(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

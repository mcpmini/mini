//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

func newRouteTestServer(mode transport.ToolMode) *Server {
	return &Server{
		logger:   discardLogger(),
		toolMode: mode,
		sessions: newSessionStore(clock.NewFake()),
	}
}

func TestRouteErrorResponseMapsErrorClassesToJSONRPCCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"method not found", fmt.Errorf("%w: nope", errMethodNotFound), transport.CodeMethodNotFound},
		{"invalid params", fmt.Errorf("%w: bad", errInvalidParams), transport.CodeInvalidParams},
		{"anything else", errors.New("upstream exploded"), transport.CodeInternalError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := routeErrorResponse(7, tc.err)
			if resp.ID != 7 || resp.Error == nil || resp.Error.Code != tc.code || resp.Error.Message != tc.err.Error() {
				t.Fatalf("response = %+v, want id 7, code %d, message %q", resp, tc.code, tc.err)
			}
		})
	}
}

func TestBuildRouteResponseReportsUnencodableResultAsInternalError(t *testing.T) {
	resp, send := buildRouteResponse(3, make(chan int), nil)
	if !send || resp.Error == nil || resp.Error.Code != transport.CodeInternalError ||
		!strings.HasPrefix(resp.Error.Message, "marshal result:") {
		t.Fatalf("response = %+v, want an internal marshal error", resp)
	}
}

func TestHandleCancelledCancelsTheNamedRequestAndListen(t *testing.T) {
	session := newSession(clock.NewFake())
	cancelled, cancel := context.WithCancel(context.Background())
	defer cancel()
	other, cancelOther := context.WithCancel(context.Background())
	defer cancelOther()
	session.registerInFlight(json.RawMessage(`7`), cancel)
	session.registerInFlight(json.RawMessage(`"7"`), cancelOther)
	session.listens["7"] = func() {}
	session.listenTools["7"] = true
	session.listens[`"7"`] = func() {}
	session.listenTools[`"7"`] = true

	handleCancelled(json.RawMessage(`{"requestId":7}`), session)

	if cancelled.Err() == nil {
		t.Fatal("in-flight request 7 was not cancelled")
	}
	if other.Err() != nil {
		t.Fatal(`request "7" was cancelled by a cancellation for numeric id 7`)
	}
	if _, open := session.listenTools["7"]; open {
		t.Fatal("listen 7 still receives tool changes after cancellation")
	}
	if _, open := session.listens["7"]; open {
		t.Fatal("listen 7 is still registered after cancellation")
	}
	if _, open := session.listens[`"7"`]; !open {
		t.Fatal(`listen "7" was removed by a cancellation for numeric id 7`)
	}
}

func TestHandleCancelledIgnoresParamsWithoutRequestID(t *testing.T) {
	for _, params := range []string{`not json`, `{}`, `{"reason":"x"}`} {
		t.Run(params, func(t *testing.T) {
			session := newSession(clock.NewFake())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session.registerInFlight(nil, cancel)
			handleCancelled(json.RawMessage(params), session)
			if ctx.Err() != nil {
				t.Fatal("a cancellation without a request id cancelled a request")
			}
		})
	}
}

func TestResolveToolModeFallsBackToServerDefault(t *testing.T) {
	tests := []struct {
		signal string
		want   transport.ToolMode
	}{
		{transport.ToolModeCompactValue, transport.ToolModeCompact},
		{transport.ToolModeProxyValue, transport.ToolModeProxy},
		{"", transport.ToolModeCompact},
		{"turbo", transport.ToolModeCompact},
	}
	srv := newRouteTestServer(transport.ToolModeCompact)
	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			if got := srv.resolveToolMode(tc.signal); got != tc.want {
				t.Fatalf("resolveToolMode(%q) = %v, want %v", tc.signal, got, tc.want)
			}
		})
	}
	proxy := newRouteTestServer(transport.ToolModeProxy)
	for _, signal := range []string{"", "turbo"} {
		if got := proxy.resolveToolMode(signal); got != transport.ToolModeProxy {
			t.Fatalf("signal %q on a proxy server resolved to %v, want the server's proxy default", signal, got)
		}
	}
}

func TestHandleInitializeKeepsTheFirstToolMode(t *testing.T) {
	srv := newRouteTestServer(transport.ToolModeProxy)
	session := newSession(clock.NewFake())
	initialize := func(params string) transport.InitializeResult {
		t.Helper()
		result, err := srv.handleInitialize(json.RawMessage(params), session)
		if err != nil {
			t.Fatal(err)
		}
		return result.(transport.InitializeResult)
	}
	first := initialize(`{"_mini_tool_mode":"compact"}`)
	second := initialize(`{"_mini_tool_mode":"proxy"}`)
	if session.toolMode() != transport.ToolModeCompact {
		t.Fatalf("session mode = %v after re-initialize, want the first compact mode", session.toolMode())
	}
	if first.Instructions != compactInitInstructions || second.Instructions != compactInitInstructions {
		t.Fatalf(
			"instructions do not follow the session's compact mode: %q / %q",
			first.Instructions,
			second.Instructions,
		)
	}
}

func TestHandleInitializeWithMalformedParamsUsesDefaults(t *testing.T) {
	srv := newRouteTestServer(transport.ToolModeProxy)
	session := newSession(clock.NewFake())
	result, err := srv.handleInitialize(json.RawMessage(`[1,2]`), session)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(transport.InitializeResult)
	if got.ProtocolVersion != transport.LatestLegacyVersion || got.Instructions != proxyInitInstructions ||
		session.toolMode() != transport.ToolModeProxy {
		t.Fatalf("initialize with malformed params = %+v in mode %v, want proxy defaults", got, session.toolMode())
	}
}

func TestAwaitInitializationGatesRequests(t *testing.T) {
	srv := newRouteTestServer(transport.ToolModeProxy)
	request := func(method string) transport.Request { return transport.Request{Method: method} }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	t.Run("discover probes before initialize on stdio", func(t *testing.T) {
		if !srv.awaitInitialization(cancelled, request("server/discover"), newSession(clock.NewFake())) {
			t.Fatal("stdio server/discover was gated on initialization")
		}
	})
	t.Run("discover on an HTTP session waits for initialize", func(t *testing.T) {
		session := newSession(clock.NewFake())
		session.httpSession.Store(true)
		if srv.awaitInitialization(cancelled, request("server/discover"), session) {
			t.Fatal("HTTP server/discover passed the initialization gate uninitialized")
		}
	})
	waiting := func(t *testing.T, end func(*Session)) (passed bool, waited time.Duration) {
		t.Helper()
		synctest.Test(t, func(t *testing.T) {
			session := newSession(clock.NewFake())
			done := make(chan bool, 1)
			start := time.Now()
			go func() { done <- srv.awaitInitialization(context.Background(), request("tools/list"), session) }()
			synctest.Wait()
			end(session)
			passed, waited = <-done, time.Since(start)
		})
		return passed, waited
	}
	t.Run("a waiting request proceeds once initialize completes", func(t *testing.T) {
		if passed, waited := waiting(t, (*Session).markInitialized); !passed || waited != 0 {
			t.Fatalf("passed = %v after %v, want passed as soon as initialize completed", passed, waited)
		}
	})
	t.Run("a waiting request fails as soon as serving ends", func(t *testing.T) {
		if passed, waited := waiting(t, (*Session).markAborted); passed || waited != 0 {
			t.Fatalf("passed = %v after %v, want rejected as soon as serving ended, not at the timeout", passed, waited)
		}
	})
}

func TestHandleToolsCallSeparatesProtocolErrorsFromToolErrors(t *testing.T) {
	session := newSession(clock.NewFake())
	session.setToolModeOnce(transport.ToolModeCompact)
	srv := newRouteTestServer(transport.ToolModeCompact)
	for _, params := range []string{`not json`, `{"name":"no_such_tool"}`} {
		t.Run(params, func(t *testing.T) {
			if _, err := srv.handleToolsCall(context.Background(), json.RawMessage(params), session); !errors.Is(
				err, errInvalidParams) {
				t.Fatalf("handleToolsCall(%s) error = %v, want invalid params", params, err)
			}
		})
	}
	t.Run("missing name", func(t *testing.T) {
		_, err := srv.handleToolsCall(context.Background(), json.RawMessage(`{"arguments":{}}`), session)
		if !errors.Is(err, errInvalidParams) || !strings.Contains(err.Error(), "requires name") {
			t.Fatalf("tools/call without a name error = %v, want invalid params naming the missing name", err)
		}
	})
	t.Run("handler failure", func(t *testing.T) {
		result, err := srv.handleToolsCall(
			context.Background(), json.RawMessage(`{"name":"list","arguments":"not an object"}`), session,
		)
		if err != nil {
			t.Fatalf("handler failure became a protocol error: %v", err)
		}
		if result.(map[string]any)["isError"] != true {
			t.Fatalf("handler failure result = %+v, want an isError tool result", result)
		}
	})
}

func TestNormalizeToolCallResult(t *testing.T) {
	failed := &response.Envelope{Error: "upstream_error", Message: "boom"}
	tests := []struct {
		name    string
		result  any
		isError bool
		text    string
	}{
		{name: "error envelope", result: failed, isError: true, text: mustJSON(failed)},
		{name: "formatted error", result: formattedEnvelope{text: "bad", isError: true}, isError: true, text: "bad"},
		{name: "formatted success", result: formattedEnvelope{text: "good"}, isError: false, text: "good"},
		{name: "string", result: "plain", isError: false, text: "plain"},
		{name: "value", result: map[string]int{"n": 1}, isError: false, text: `{"n":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeToolCallResult(tc.result).(map[string]any)
			content, hasContent := got["content"].([]map[string]any)
			if !ok || !hasContent || len(content) == 0 {
				t.Fatalf("result = %#v, want a map with a content block", got)
			}
			if text := content[0]["text"]; (got["isError"] == true) != tc.isError || text != tc.text {
				t.Fatalf("result = %+v, want isError=%v text %q", got, tc.isError, tc.text)
			}
		})
	}
	t.Run("upstream result passes through", func(t *testing.T) {
		raw := upstreamResult(`{"content":[],"structuredContent":{"a":1}}`)
		if got, ok := normalizeToolCallResult(raw).(upstreamResult); !ok || string(got) != string(raw) {
			t.Fatalf("upstream result = %v, want it unchanged", got)
		}
	})
}

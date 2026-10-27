//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

func TestCallPerSession_UnsupportedArgumentsFailBeforeUpstreamCall(t *testing.T) {
	conn := &transport.FakeConnection{
		LastParams: json.RawMessage(`{"sentinel":true}`),
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
		},
	}
	session := newSession(clock.System())
	session.GetOrSetConn("svc", conn)
	srv := &Server{}

	_, err := srv.callPerSession(context.Background(), dispatchParams{
		Upstream: &upstreamServer{cfg: config.ServerConfig{Name: "svc", SessionMode: config.SessionModePerSession}},
		Tool:     "test",
		Params:   map[string]any{"unsupported": make(chan int)},
		Session:  session,
	})
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("callPerSession error = %v, want json.UnsupportedTypeError", err)
	}
	if string(conn.LastParams) != `{"sentinel":true}` {
		t.Fatalf("upstream Call params = %s, want unchanged sentinel because Call was not invoked", conn.LastParams)
	}
}

func TestCallPerSession_toolErrorResultReturnsRawWithToolError(t *testing.T) {
	result := json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)
	conn := &transport.FakeConnection{Responses: map[string]json.RawMessage{"tools/call": result}}
	session := newSession(clock.System())
	session.GetOrSetConn("svc", conn)
	srv := &Server{}

	raw, err := srv.callPerSession(context.Background(), dispatchParams{
		Upstream: &upstreamServer{cfg: config.ServerConfig{Name: "svc", SessionMode: config.SessionModePerSession}},
		Tool:     "test",
		Session:  session,
	})
	if !errors.Is(err, invoke.ErrToolReturnedError) {
		t.Fatalf("callPerSession error = %v, want ErrToolReturnedError", err)
	}
	if string(raw) != string(result) {
		t.Fatalf("callPerSession raw = %s, want the upstream result unchanged", raw)
	}
}

func TestCallPerSession_forwardsRetryFieldsAndReturnsTheInputRequest(t *testing.T) {
	result := json.RawMessage(`{"resultType":"input_required","inputRequests":{},"requestState":"s1"}`)
	conn := &transport.FakeConnection{Responses: map[string]json.RawMessage{"tools/call": result}}
	session := newSession(clock.System())
	session.GetOrSetConn("svc", conn)
	srv := &Server{}

	raw, err := srv.callPerSession(context.Background(), dispatchParams{
		Upstream: &upstreamServer{cfg: config.ServerConfig{Name: "svc", SessionMode: config.SessionModePerSession}},
		Tool:     "test",
		Session:  session,
		MRTR: &transport.ToolCallMRTR{
			InputResponses: json.RawMessage(`{"r1":{}}`),
			RequestState:   json.RawMessage(`"s0"`),
		},
	})
	if !errors.Is(err, invoke.ErrInputRequired) || string(raw) != string(result) {
		t.Fatalf("callPerSession = %s, %v; want the upstream input request and ErrInputRequired", raw, err)
	}
	want := `{"name":"test","arguments":null,"inputResponses":{"r1":{}},"requestState":"s0"}`
	if string(conn.LastParams) != want {
		t.Fatalf("per-session upstream received %s, want %s", conn.LastParams, want)
	}
}

func TestDispatchPerSession_CapabilityErrorPreservesConnectionForNextCall(t *testing.T) {
	upstreamErr := &transport.RPCError{
		Code: transport.CodeMissingRequiredClientCapability, Message: "form elicitation required",
		Data: json.RawMessage(`{"requiredCapabilities":{"elicitation":{"form":{}}}}`),
	}
	result := json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"confirmed"}]}`)
	conn := &transport.FakeConnection{
		Responses: map[string]json.RawMessage{"tools/call": result}, Err: upstreamErr,
	}
	session := newSession(clock.System())
	t.Cleanup(session.Close)
	session.GetOrSetConn("svc", conn)
	srv := newInternalTestServer(t)
	call := dispatchParams{
		Upstream: &upstreamServer{cfg: config.ServerConfig{Name: "svc", SessionMode: config.SessionModePerSession}},
		Tool:     "confirm", Session: session, MRTR: &transport.ToolCallMRTR{},
	}

	if raw, err := srv.dispatchRawCall(t.Context(), call); !errors.Is(err, upstreamErr) || len(raw) != 0 {
		t.Fatalf("first call = %s, %#v; want the original capability error and no result", raw, err)
	}
	if session.Conn("svc") != conn || conn.Closed {
		t.Fatal("capability error evicted or closed the per-session connection")
	}

	conn.SetError(nil)
	raw, err := srv.dispatchRawCall(t.Context(), call)
	if err != nil || string(raw) != string(result) {
		t.Fatalf("follow-up call = %s, %v; want successful result on the retained connection", raw, err)
	}
	if session.Conn("svc") != conn || conn.Closed {
		t.Fatal("follow-up call did not retain the same usable connection")
	}
}

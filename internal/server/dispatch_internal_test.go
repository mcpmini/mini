//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
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

//go:build test

package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func TestWaitResponseSkipsRepliesToEarlierRequests(t *testing.T) {
	peer := &agentPeer{replies: make(chan agentReply, 2)}
	peer.replies <- agentReply{response: transport.Response{ID: float64(1), Result: json.RawMessage(`"late"`)}}
	peer.replies <- agentReply{response: transport.Response{ID: float64(2), Result: json.RawMessage(`"current"`)}}

	got, err := peer.waitResponse(2, time.Second)
	if err != nil {
		t.Fatalf("waitResponse(2) error = %v", err)
	}
	if string(got.Result) != `"current"` {
		t.Fatalf("waitResponse(2) result = %s, want the reply for request 2", got.Result)
	}
}

func TestToolResultError(t *testing.T) {
	tests := []struct {
		name    string
		result  map[string]any
		wantErr bool
	}{
		{name: "complete result", result: map[string]any{"content": []any{}}, wantErr: false},
		{name: "tool error", result: map[string]any{"isError": true, "content": []any{}}, wantErr: true},
		{name: "protocol error", result: map[string]any{"error": map[string]any{"code": -32602}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := toolResultError("tool", test.result); (err != nil) != test.wantErr {
				t.Fatalf("toolResultError() error = %v, want error %v", err, test.wantErr)
			}
		})
	}
}

func TestUpstreamReady(t *testing.T) {
	status := func(text string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	}
	tests := []struct {
		name   string
		result map[string]any
		want   bool
	}{
		{
			name:   "conf listed with no tools",
			result: status(`{"servers":{"conf":{"tools":0}},"starting":[]}`),
			want:   true,
		},
		{name: "conf still starting", result: status(`{"servers":{},"starting":["conf"]}`), want: false},
		{name: "no content", result: map[string]any{}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := upstreamReady(test.result); got != test.want {
				t.Fatalf("upstreamReady() = %v, want %v", got, test.want)
			}
		})
	}
}

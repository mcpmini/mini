//go:build test

package main

import (
	"encoding/json"
	"strings"
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

func TestToolErrorsAreFailures(t *testing.T) {
	rpcError := transport.Response{Error: &transport.RPCError{Code: -32602, Message: "bad"}}
	if _, err := decodeAgentResponse(rpcError); err == nil {
		t.Error("decodeAgentResponse(JSON-RPC error) error = nil, want error")
	}
	if _, err := completedToolResult("tool", map[string]any{"isError": true, "content": []any{}}); err == nil {
		t.Error("completedToolResult(isError) error = nil, want error")
	}
	got, err := completedToolResult("tool", map[string]any{"resultType": "input_required"})
	if err != nil || got["resultType"] != "input_required" {
		t.Errorf("completedToolResult(input_required) = %v, %v; want the result unchanged", got, err)
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

func TestAgentToolRetryPreservesWireStateAndMetadata(t *testing.T) {
	previous := map[string]any{"requestState": `{"opaque":"\\value"}`, "inputRequests": map[string]any{
		"roots": map[string]any{"method": "roots/list"},
	}}
	extra, err := nextRoundExtra(previous)
	if err != nil {
		t.Fatal(err)
	}
	params := transport.NewToolCallParams("conf__retry", map[string]any{}, extra)
	params.Meta = agentMeta()
	request, err := makeAgentRequest(7, "tools/call", params)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &fields); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := json.Unmarshal(fields["requestState"], &state); err != nil || state != previous["requestState"] {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if string(fields["inputResponses"]) != `{"roots":{"roots":[]}}` {
		t.Fatalf("inputResponses=%s", fields["inputResponses"])
	}
	assertFormCapability(t, fields["_meta"])
}

func TestAgentFreshCallOmitsRetryFields(t *testing.T) {
	fresh := transport.NewToolCallParams("conf__fresh", map[string]any{}, nil)
	fresh.Meta = agentMeta()
	encoded, err := marshalAgentParams(fresh)
	if err != nil || strings.Contains(string(encoded), "requestState") ||
		strings.Contains(string(encoded), "inputResponses") {
		t.Fatalf("fresh params=%s err=%v", encoded, err)
	}
}

func assertFormCapability(t *testing.T, data json.RawMessage) {
	t.Helper()
	var meta struct {
		Capabilities struct {
			Elicitation json.RawMessage `json:"elicitation"`
		} `json:"io.modelcontextprotocol/clientCapabilities"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || string(meta.Capabilities.Elicitation) != `{"form":{}}` {
		t.Fatalf("metadata=%s err=%v", data, err)
	}
}

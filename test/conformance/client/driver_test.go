//go:build test

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func TestParseToolTable(t *testing.T) {
	output := "TOOL  HINTS\nalpha  read-only\nbeta  destructive\n\nDetails: mini ls conf <tool>\n"
	if got := parseToolTable(output); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("parseToolTable() = %#v, want alpha and beta", got)
	}
	if got := parseToolTable("No tools available.\n"); got != nil {
		t.Fatalf("parseToolTable(empty table) = %#v, want nil", got)
	}
}

func TestFailedMiniCommandsAreErrors(t *testing.T) {
	failed := cliResult{stdout: "TOOL HINTS\nalpha\n", err: errors.New("exit status 42")}
	if err := failed.failure("mini ls"); err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("failure() = %v, want an error carrying mini's output", err)
	}
	if err := (cliResult{stdout: "ok"}).failure("mini ls"); err != nil {
		t.Fatalf("failure() = %v for a successful command", err)
	}
}

type agentInput struct{ bytes.Buffer }

func (*agentInput) Close() error { return nil }

func TestAgentCallRejectsToolAndProtocolErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response transport.Response
		want     string
	}{
		{name: "tool error", response: transport.Response{Result: json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"denied"}]}`)}, want: "tool error: denied"},
		{name: "protocol error", response: transport.Response{Error: &transport.RPCError{Code: -32602, Message: "bad"}}, want: "protocol error -32602: bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := scriptedAgent(tc.response)
			_, err := d.callTool(toolCall{Name: "tool"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("callTool() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func scriptedAgent(response transport.Response) *agentDriver {
	response.ID = float64(1)
	d := &agentDriver{input: &agentInput{}, replies: make(chan agentReply, 1)}
	d.replies <- agentReply{response: response}
	return d
}

func TestAgentCallPreservesInputRequests(t *testing.T) {
	d := scriptedAgent(
		transport.Response{
			Result: json.RawMessage(
				`{"resultType":"input_required","requestState":"opaque","inputRequests":{"r":{"method":"roots/list"}}}`,
			),
		},
	)
	got, err := d.callTool(toolCall{Name: "tool"})
	if err != nil || got.ResultType != transport.ResultInputRequired ||
		string(got.RequestState) != `"opaque"` || got.InputRequests["r"].Method != "roots/list" {
		t.Fatalf("callTool() = %+v, %v; want pending input request and state", got, err)
	}
}

func TestMiniCallOutput(t *testing.T) {
	if err := callOutput("tool", cliResult{stdout: "not json"}); err == nil {
		t.Error("callOutput(not JSON) error = nil")
	}
	if err := callOutput("tool", cliResult{stdout: `{"data":"ok"}`, err: errors.New("exit status 1")}); err == nil {
		t.Error("callOutput(failed command) error = nil")
	}
	if err := callOutput("tool", cliResult{stdout: `{"data":"ok"}`}); err != nil {
		t.Errorf("callOutput(success) error = %v", err)
	}
}

func TestParseMode(t *testing.T) {
	for raw, want := range map[string]mode{"": agentMode, "agent": agentMode, "cli": cliMode} {
		if got, err := parseMode(raw); err != nil || got != want {
			t.Errorf("parseMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := parseMode("http"); err == nil {
		t.Error("parseMode(http) error = nil")
	}
}

func TestWaitResponseSkipsRepliesToEarlierRequests(t *testing.T) {
	d := &agentDriver{replies: make(chan agentReply, 2)}
	d.replies <- agentReply{response: transport.Response{ID: float64(1), Result: json.RawMessage(`"late"`)}}
	d.replies <- agentReply{response: transport.Response{ID: float64(2), Result: json.RawMessage(`"current"`)}}
	got, err := d.waitResponse(2, time.Second)
	if err != nil || string(got.Result) != `"current"` {
		t.Fatalf("waitResponse(2) = %s, %v; want the reply for request 2", got.Result, err)
	}
}

func TestUpstreamReady(t *testing.T) {
	status := func(text string) toolResult {
		return toolResult{Content: []transport.ContentItem{{Type: "text", Text: text}}}
	}
	tests := []struct {
		name   string
		result toolResult
		want   bool
	}{
		{
			name:   "conf listed with no tools",
			result: status(`{"servers":{"conf":{"tools":0}},"starting":[]}`),
			want:   true,
		},
		{name: "conf still starting", result: status(`{"servers":{},"starting":["conf"]}`)},
		{name: "no content", result: toolResult{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := upstreamReady(test.result); got != test.want {
				t.Fatalf("upstreamReady() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAgentToolCallCarriesMetadataAndOnlyRetryFields(t *testing.T) {
	fresh, err := json.Marshal(agentToolCall(toolCall{Name: "conf__fresh", Arguments: map[string]any{}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fresh), "requestState") || strings.Contains(string(fresh), "inputResponses") {
		t.Fatalf("fresh call carries retry fields: %s", fresh)
	}
	round := &transport.ToolCallMRTR{InputResponses: json.RawMessage(`{"r":{}}`), RequestState: json.RawMessage(`"s"`)}
	retry, err := json.Marshal(agentToolCall(toolCall{Name: "conf__retry", Arguments: map[string]any{}, Round: round}))
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		RequestState   json.RawMessage `json:"requestState"`
		InputResponses json.RawMessage `json:"inputResponses"`
		Meta           struct {
			Capabilities struct {
				Elicitation json.RawMessage `json:"elicitation"`
			} `json:"io.modelcontextprotocol/clientCapabilities"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(retry, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields.RequestState) != `"s"` || string(fields.InputResponses) != `{"r":{}}` ||
		string(fields.Meta.Capabilities.Elicitation) != `{"form":{}}` {
		t.Fatalf("retry call = %s", retry)
	}
}

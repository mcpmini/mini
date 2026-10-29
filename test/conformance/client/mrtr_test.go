//go:build test

package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestElicitationAnswerValues(t *testing.T) {
	var schema objectSchema
	err := json.Unmarshal([]byte(`{"properties":{
		"choice":{"enum":["first","second"]},"flag":{"type":"boolean"},"integer":{"type":"integer","minimum":3},
		"number":{"type":"number"},"email":{"type":"string","format":"email"},"uri":{"format":"uri"},
		"date":{"format":"date"},"dateTime":{"format":"date-time"},"name":{"type":"string"}}}`), &schema)
	if err != nil {
		t.Fatal(err)
	}
	want := elicitationResult{Action: "accept", Content: map[string]any{
		"choice": "first", "flag": true, "integer": float64(3), "number": 1, "email": "a@example.com",
		"uri": "https://example.com", "date": "2000-01-01", "dateTime": "2000-01-01T00:00:00Z", "name": "Ada",
	}}
	if got := elicitationAnswer(schema); !reflect.DeepEqual(got, want) {
		t.Fatalf("elicitationAnswer() = %#v, want %#v", got, want)
	}
}

func TestInputResponsesAnswerEachKnownMethod(t *testing.T) {
	requests := map[string]inputRequest{
		"e": {
			Method: "elicitation/create",
			Params: json.RawMessage(`{"requestedSchema":{"properties":{"name":{"type":"string"}}}}`),
		},
		"s":       {Method: "sampling/createMessage"},
		"r":       {Method: "roots/list"},
		"unknown": {Method: "unknown"},
	}
	got, err := inputResponses(requests)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"e":{"action":"accept","content":{"name":"Ada"}},"r":{"roots":[]},` +
		`"s":{"role":"assistant","content":{"type":"text","text":"ok"},"model":"harness"}}`
	if string(encoded) != want {
		t.Fatalf("inputResponses() = %s, want %s", encoded, want)
	}
}

func TestNextRoundEchoesRequestStateVerbatim(t *testing.T) {
	state := json.RawMessage(`"{\"opaque\":\"\\\\value\"}"`)
	next, err := nextRound(toolResult{
		RequestState: state, InputRequests: map[string]inputRequest{"roots": {Method: "roots/list"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(next.RequestState) != string(state) {
		t.Fatalf("RequestState = %s, want %s", next.RequestState, state)
	}
	if string(next.InputResponses) != `{"roots":{"roots":[]}}` {
		t.Fatalf("InputResponses = %s", next.InputResponses)
	}
}

type scriptedDriver struct {
	recordingDriver
	results []toolResult
}

func (d *scriptedDriver) callTool(call toolCall) (toolResult, error) {
	d.calls = append(d.calls, call)
	result := d.results[0]
	d.results = d.results[1:]
	return result, nil
}

func TestCompleteRoundsCallsTheUnrelatedToolBetweenRounds(t *testing.T) {
	pending := toolResult{ResultType: transport.ResultInputRequired, RequestState: json.RawMessage(`"s"`)}
	done := toolResult{ResultType: transport.ResultComplete}
	d := &scriptedDriver{results: []toolResult{pending, done, done}}
	if err := completeRounds(d, "stateful", "unrelated"); err != nil {
		t.Fatalf("completeRounds() error = %v", err)
	}
	var names []string
	for _, call := range d.calls {
		names = append(names, call.Name)
	}
	if !reflect.DeepEqual(names, []string{"stateful", "unrelated", "stateful"}) {
		t.Fatalf("calls = %v", names)
	}
	if d.calls[1].Round != nil || d.calls[2].Round == nil || string(d.calls[2].Round.RequestState) != `"s"` {
		t.Fatalf("only the retry carries the round: %+v", d.calls)
	}
}

func TestCompleteRoundsStopsAfterTheRoundLimit(t *testing.T) {
	pending := toolResult{ResultType: transport.ResultInputRequired}
	d := &scriptedDriver{results: []toolResult{pending, pending, pending, pending, pending}}
	if err := completeRounds(d, "endless", ""); err == nil {
		t.Fatal("completeRounds() error = nil for a tool that never completes")
	}
}

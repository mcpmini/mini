//go:build test

package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestPlanFor(t *testing.T) {
	tools := []transport.ToolDefinition{
		schemaTestTool(schemaTool, "value", `"string"`), schemaTestTool("ordinary", "count", `"integer"`),
	}
	tests := []struct {
		name string
		sc   scenario
		want []toolCall
	}{
		{
			name: "context tool calls take precedence",
			sc: scenario{Name: "tools_call", Context: scenarioContext{ToolCalls: []toolCall{
				{Name: "chosen", Arguments: map[string]any{"x": "y"}}, {Name: "bare"},
			}}},
			want: []toolCall{
				{Name: "chosen", Arguments: map[string]any{"x": "y"}},
				{Name: "bare", Arguments: map[string]any{}},
			},
		},
		{
			name: "schema echo returns the schema unchanged",
			sc:   scenario{Name: "json-schema-2020-12-preservation"},
			want: []toolCall{{Name: schemaEchoTool, Arguments: map[string]any{"schema": tools[0].InputSchema}}},
		},
		{name: "ref scenario makes no calls", sc: scenario{Name: "json-schema-ref-no-deref"}},
		{
			name: "tools_call adds numbers with the first tool",
			sc:   scenario{Name: "tools_call"},
			want: []toolCall{{Name: schemaTool, Arguments: addNumbersArguments}},
		},
		{
			name: "auth calls the first tool with no arguments",
			sc:   scenario{Name: "auth/metadata-default"},
			want: []toolCall{{Name: schemaTool, Arguments: map[string]any{}}},
		},
		{
			name: "other scenarios derive each tool's arguments",
			sc:   scenario{Name: "request-metadata"},
			want: []toolCall{
				{Name: schemaTool, Arguments: map[string]any{"value": "x"}},
				{Name: "ordinary", Arguments: map[string]any{"count": 1}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := planFor(test.sc, tools); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("planFor() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func schemaTestTool(name, property, propertyType string) transport.ToolDefinition {
	schema := `{"type":"object","properties":{"` + property + `":{"type":` + propertyType + `}}}`
	return transport.ToolDefinition{Name: name, InputSchema: json.RawMessage(schema)}
}

func TestArgumentsForSchemaTypes(t *testing.T) {
	tool := transport.ToolDefinition{InputSchema: json.RawMessage(`{"properties":{
		"a":{"type":"integer"},"b":{"type":"number"},"integer":{"type":"integer"},"number":{"type":"number"},
		"enabled":{"type":"boolean"},"text":{"type":"string"},"nullable":{"type":["string","null"]},
		"unspecified":{},"ignored":{"type":"array"}}}`)}
	want := map[string]any{
		"a": 2, "b": 3, "integer": 1, "number": 1, "enabled": true, "text": "x", "nullable": "x", "unspecified": "x",
	}
	if got := argumentsFor(tool); !reflect.DeepEqual(got, want) {
		t.Fatalf("argumentsFor() = %#v, want %#v", got, want)
	}
}

type recordingDriver struct {
	calls []toolCall
}

func (d *recordingDriver) tools() ([]transport.ToolDefinition, error) { return nil, nil }

func (d *recordingDriver) callTool(call toolCall) (toolResult, error) {
	d.calls = append(d.calls, call)
	return toolResult{ResultType: transport.ResultComplete}, nil
}

func (d *recordingDriver) close() error { return nil }

func TestCallAllMakesEveryPlannedCall(t *testing.T) {
	d := &recordingDriver{}
	plan := []toolCall{{Name: "first"}, {Name: "second"}}
	if err := callAll(d, plan); err != nil {
		t.Fatalf("callAll() error = %v", err)
	}
	if !reflect.DeepEqual(d.calls, plan) {
		t.Fatalf("calls = %#v, want %#v", d.calls, plan)
	}
}

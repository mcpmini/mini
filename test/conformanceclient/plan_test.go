//go:build test

package main

import (
	"reflect"
	"testing"
)

func TestSelectPlan(t *testing.T) {
	tools := []tool{schemaTestTool(focalTool, "value", "string"), schemaTestTool("ordinary", "count", "integer")}
	tests := []struct {
		name     string
		scenario string
		ctx      scenarioContext
		agent    bool
		want     []plannedCall
	}{
		{
			name: "context tool calls take precedence", scenario: "tools_call", agent: true,
			ctx:  scenarioContext{ToolCalls: []toolCall{{Name: "chosen", Arguments: map[string]any{"x": "y"}}}},
			want: []plannedCall{{Name: "chosen", Arguments: map[string]any{"x": "y"}}},
		},
		{
			name: "schema echo is agent only", scenario: "json-schema-2020-12-preservation", agent: true,
			want: []plannedCall{{Name: echoTool, Arguments: map[string]any{"schema": tools[0].InputSchema}}},
		},
		{
			name: "default plan derives each tool arguments", scenario: "request-metadata", agent: true,
			want: []plannedCall{
				{Name: focalTool, Arguments: map[string]any{"value": "x"}},
				{Name: "ordinary", Arguments: map[string]any{"count": 1}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := selectPlan(planParams{
				Scenario: test.scenario, Context: test.ctx, Tools: tools, Agent: test.agent,
			})
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selectPlan() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func schemaTestTool(name, property, propertyType string) tool {
	return tool{
		Name: name,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{
				property: map[string]any{"type": propertyType},
			},
		},
	}
}

func TestArgumentsForSchemaTypes(t *testing.T) {
	item := tool{InputSchema: map[string]any{"properties": map[string]any{
		"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "number"},
		"integer": map[string]any{"type": "integer"}, "number": map[string]any{"type": "number"},
		"enabled": map[string]any{"type": "boolean"}, "text": map[string]any{"type": "string"},
		"unspecified": map[string]any{}, "ignored": map[string]any{"type": "array"},
	}}}
	want := map[string]any{"a": 2, "b": 3, "integer": 1, "number": 1, "enabled": true, "text": "x", "unspecified": "x"}
	if got := argumentsFor(item); !reflect.DeepEqual(got, want) {
		t.Fatalf("argumentsFor() = %#v, want %#v", got, want)
	}
}

func TestElicitationAnswerValues(t *testing.T) {
	properties := map[string]any{
		"choice":   map[string]any{"enum": []any{"first", "second"}},
		"flag":     map[string]any{"type": "boolean"},
		"integer":  map[string]any{"type": "integer", "minimum": float64(3)},
		"number":   map[string]any{"type": "number"},
		"email":    map[string]any{"type": "string", "format": "email"},
		"uri":      map[string]any{"format": "uri"},
		"date":     map[string]any{"format": "date"},
		"dateTime": map[string]any{"format": "date-time"},
		"name":     map[string]any{"type": "string"},
	}
	want := map[string]any{
		"action": "accept", "content": map[string]any{
			"choice": "first", "flag": true, "integer": float64(3), "number": 1,
			"email": "a@example.com", "uri": "https://example.com", "date": "2000-01-01",
			"dateTime": "2000-01-01T00:00:00Z", "name": "Ada",
		},
	}
	got := elicitationAnswer(map[string]any{"requestedSchema": map[string]any{"properties": properties}})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("elicitationAnswer() = %#v, want %#v", got, want)
	}
}

func TestInputResponsesMethods(t *testing.T) {
	input := map[string]any{"inputRequests": map[string]any{
		"e":       elicitationInputRequest(),
		"s":       map[string]any{"method": "sampling/createMessage"},
		"r":       map[string]any{"method": "roots/list"},
		"unknown": map[string]any{"method": "unknown"},
	}}
	want := map[string]any{
		"e": map[string]any{"action": "accept", "content": map[string]any{"name": "Ada"}},
		"s": samplingResponse(),
		"r": map[string]any{"roots": []any{}},
	}
	if got := inputResponses(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("inputResponses() = %#v, want %#v", got, want)
	}
}

func elicitationInputRequest() map[string]any {
	return map[string]any{
		"method": "elicitation/create",
		"params": map[string]any{
			"requestedSchema": map[string]any{
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
	}
}

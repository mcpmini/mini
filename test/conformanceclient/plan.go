package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	focalTool = "json_schema_2020_12_tool"
	echoTool  = "json_schema_echo"
)

type tool struct {
	Name        string         `json:"name"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type scenarioContext struct {
	ToolCalls []toolCall `json:"toolCalls"`
}

type plannedCall struct {
	Name      string
	Arguments map[string]any
}

type planParams struct {
	Scenario string
	Context  scenarioContext
	Tools    []tool
	Agent    bool
}

func selectPlan(params planParams) []plannedCall {
	if len(params.Context.ToolCalls) > 0 {
		return contextPlan(params.Context.ToolCalls)
	}
	if params.Scenario == "json-schema-ref-no-deref" {
		return nil
	}
	if params.Scenario == "json-schema-2020-12-preservation" && params.Agent {
		return schemaEchoPlan(params.Tools)
	}
	if params.Scenario == "tools_call" || params.Scenario == "initialize" || isAuthScenario(params.Scenario) {
		if len(params.Tools) == 0 {
			return nil
		}
		args := map[string]any{"a": 2, "b": 3}
		if isAuthScenario(params.Scenario) {
			args = map[string]any{}
		}
		return []plannedCall{{Name: params.Tools[0].Name, Arguments: args}}
	}
	return schemaArgumentPlan(params.Tools)
}

func contextPlan(calls []toolCall) []plannedCall {
	out := make([]plannedCall, 0, len(calls))
	for _, call := range calls {
		args := call.Arguments
		if args == nil {
			args = map[string]any{}
		}
		out = append(out, plannedCall{Name: call.Name, Arguments: args})
	}
	return out
}

func schemaEchoPlan(tools []tool) []plannedCall {
	for _, item := range tools {
		if item.Name == focalTool && item.InputSchema != nil {
			return []plannedCall{{Name: echoTool, Arguments: map[string]any{"schema": item.InputSchema}}}
		}
	}
	return nil
}

func schemaArgumentPlan(tools []tool) []plannedCall {
	out := make([]plannedCall, 0, len(tools))
	for _, item := range tools {
		out = append(out, plannedCall{Name: item.Name, Arguments: argumentsFor(item)})
	}
	return out
}

func argumentsFor(item tool) map[string]any {
	props, _ := item.InputSchema["properties"].(map[string]any)
	out := make(map[string]any)
	for key, raw := range props {
		property, _ := raw.(map[string]any)
		typeName, _ := property["type"].(string)
		switch {
		case key == "a":
			out[key] = 2
		case key == "b":
			out[key] = 3
		case typeName == "number" || typeName == "integer":
			out[key] = 1
		case typeName == "boolean":
			out[key] = true
		case typeName == "string" || typeName == "":
			out[key] = "x"
		}
	}
	return out
}

func elicitationAnswer(params map[string]any) map[string]any {
	properties, _ := nestedMap(params, "requestedSchema", "properties")
	content := make(map[string]any)
	for key, raw := range properties {
		property, _ := raw.(map[string]any)
		content[key] = elicitationValue(property)
	}
	return map[string]any{"action": "accept", "content": content}
}

func elicitationValue(property map[string]any) any {
	if values, ok := property["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	typeName, _ := property["type"].(string)
	switch typeName {
	case "boolean":
		return true
	case "integer", "number":
		if minimum, ok := property["minimum"]; ok {
			return minimum
		}
		return 1
	}
	formats := map[string]string{
		"email": "a@example.com", "uri": "https://example.com", "date": "2000-01-01",
		"date-time": "2000-01-01T00:00:00Z",
	}
	if format, _ := property["format"].(string); formats[format] != "" {
		return formats[format]
	}
	return "Ada"
}

func inputResponses(result map[string]any) map[string]any {
	requests, _ := result["inputRequests"].(map[string]any)
	out := make(map[string]any)
	for key, raw := range requests {
		request, _ := raw.(map[string]any)
		method, _ := request["method"].(string)
		params, _ := request["params"].(map[string]any)
		switch method {
		case "elicitation/create":
			out[key] = elicitationAnswer(params)
		case "sampling/createMessage":
			out[key] = samplingResponse()
		case "roots/list":
			out[key] = map[string]any{"roots": []any{}}
		}
	}
	return out
}

func samplingResponse() map[string]any {
	return map[string]any{
		"role": "assistant", "content": map[string]any{"type": "text", "text": "ok"}, "model": "harness",
	}
}

func nestedMap(value map[string]any, keys ...string) (map[string]any, bool) {
	current := value
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func decodeContext(raw string) (scenarioContext, error) {
	if raw == "" {
		return scenarioContext{}, nil
	}
	var ctx scenarioContext
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return scenarioContext{}, fmt.Errorf("decode conformance context: %w", err)
	}
	return ctx, nil
}

func isAuthScenario(scenario string) bool { return strings.HasPrefix(scenario, "auth/") }

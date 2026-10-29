package main

import (
	"bytes"
	"encoding/json"

	"github.com/mcpmini/mini/internal/transport"
)

const (
	schemaTool     = "json_schema_2020_12_tool"
	schemaEchoTool = "json_schema_echo"
)

var addNumbersArguments = map[string]any{"a": 2, "b": 3}

type objectSchema struct {
	Properties map[string]schemaProperty `json:"properties"`
}

type schemaProperty struct {
	Type    schemaType `json:"type"`
	Enum    []any      `json:"enum"`
	Minimum *float64   `json:"minimum"`
	Format  string     `json:"format"`
}

type schemaType string

func (t *schemaType) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		var types []string
		if err := json.Unmarshal(data, &types); err != nil || len(types) == 0 {
			return err
		}
		*t = schemaType(types[0])
		return nil
	}
	return json.Unmarshal(data, (*string)(t))
}

func planFor(sc scenario, tools []transport.ToolDefinition) []toolCall {
	switch {
	case len(sc.Context.ToolCalls) > 0:
		return contextPlan(sc.Context.ToolCalls)
	case sc.Name == "json-schema-ref-no-deref":
		return nil
	case sc.Name == "json-schema-2020-12-preservation":
		return schemaEchoPlan(tools)
	case sc.Name == "tools_call" || sc.Name == "initialize":
		return firstToolPlan(tools, addNumbersArguments)
	case sc.isAuth():
		return firstToolPlan(tools, map[string]any{})
	default:
		return schemaArgumentPlan(tools)
	}
}

func contextPlan(calls []toolCall) []toolCall {
	planned := make([]toolCall, 0, len(calls))
	for _, call := range calls {
		if call.Arguments == nil {
			call.Arguments = map[string]any{}
		}
		planned = append(planned, call)
	}
	return planned
}

func firstToolPlan(tools []transport.ToolDefinition, args map[string]any) []toolCall {
	if len(tools) == 0 {
		return nil
	}
	return []toolCall{{Name: tools[0].Name, Arguments: args}}
}

func schemaEchoPlan(tools []transport.ToolDefinition) []toolCall {
	for _, tool := range tools {
		if tool.Name == schemaTool && len(tool.InputSchema) > 0 {
			return []toolCall{{Name: schemaEchoTool, Arguments: map[string]any{"schema": tool.InputSchema}}}
		}
	}
	return nil
}

func schemaArgumentPlan(tools []transport.ToolDefinition) []toolCall {
	calls := make([]toolCall, 0, len(tools))
	for _, tool := range tools {
		calls = append(calls, toolCall{Name: tool.Name, Arguments: argumentsFor(tool)})
	}
	return calls
}

func argumentsFor(tool transport.ToolDefinition) map[string]any {
	var schema objectSchema
	args := make(map[string]any)
	if len(tool.InputSchema) == 0 || json.Unmarshal(tool.InputSchema, &schema) != nil {
		return args
	}
	for key, property := range schema.Properties {
		if value, ok := addNumbersArguments[key]; ok {
			args[key] = value
			continue
		}
		switch property.Type {
		case "number", "integer":
			args[key] = 1
		case "boolean":
			args[key] = true
		case "string", "":
			args[key] = "x"
		}
	}
	return args
}

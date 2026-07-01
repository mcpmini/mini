//go:build test

package server_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestProxySchema_ArgsRequiredWhenUpstreamHasRequiredFields(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "get_item",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tool := findTool(toolsList(t, srv), "svc__get_item")
	if tool == nil {
		t.Fatal("svc__get_item not found")
	}
	inSchema := tool["inputSchema"].(map[string]any)
	requireArgsRequired(t, inSchema)
	assertArgsSchema(t, inSchema, map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []any{"id"},
	})
}

func TestProxySchema_ArgsOptionalWhenNoRequiredFields(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "list_items",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"}}}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tool := findTool(toolsList(t, srv), "svc__list_items")
	if tool == nil {
		t.Fatal("svc__list_items not found")
	}
	inSchema := tool["inputSchema"].(map[string]any)
	if req, ok := inSchema["required"]; ok {
		t.Errorf("expected no top-level required when tool takes no mandatory args, got: %v", req)
	}
}

func TestProxySchema_ArgsRequiredWhenRootHasRef(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "complex_tool",
			Description: "desc",
			InputSchema: json.RawMessage(`{"$ref":"#/$defs/Input","$defs":{"Input":{"type":"object"}}}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tool := findTool(toolsList(t, srv), "svc__complex_tool")
	if tool == nil {
		t.Fatal("svc__complex_tool not found")
	}
	requireArgsRequired(t, tool["inputSchema"].(map[string]any))
}

func TestProxySchema_ArgsRequiredWhenMinPropertiesPositive(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "min_props_tool",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object","minProperties":1}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tool := findTool(toolsList(t, srv), "svc__min_props_tool")
	requireArgsRequired(t, tool["inputSchema"].(map[string]any))
}

func TestProxySchema_InputControlEnumOffersDefaultAndRaw(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	addProxyConn(t, srv, "svc", fakeConn("noop"))

	tool := findTool(toolsList(t, srv), "svc__noop")
	inSchema := tool["inputSchema"].(map[string]any)
	props := inSchema["properties"].(map[string]any)
	mini := props["__mini"].(map[string]any)
	miniProps := mini["properties"].(map[string]any)
	projection := miniProps["projection"].(map[string]any)
	enum := toStringSlice(projection["enum"])
	if len(enum) != 2 || enum[0] != "default" || enum[1] != "raw" {
		t.Errorf("expected __mini.projection enum [default raw], got: %v", enum)
	}
	if additional, _ := mini["additionalProperties"].(bool); additional {
		t.Error("expected __mini to reject additional properties")
	}
}

func TestProxySchema_OutputSchemaSynthesizedWhenAbsent(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	addProxyConn(t, srv, "svc", fakeConn("no_output_schema"))

	tool := findTool(toolsList(t, srv), "svc__no_output_schema")
	outSchema := tool["outputSchema"].(map[string]any)
	assertStableOutputShape(t, outSchema)
	data := outSchema["properties"].(map[string]any)["data"].(map[string]any)
	if _, hasType := data["type"]; hasType {
		t.Errorf("synthesized data schema should be empty besides $id, got: %v", data)
	}
}

func TestProxySchema_OutputSchemaWrapsUpstreamSchema(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:         "get_weather",
			Description:  "desc",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"temp":{"type":"number"}}}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tool := findTool(toolsList(t, srv), "svc__get_weather")
	outSchema := tool["outputSchema"].(map[string]any)
	assertStableOutputShape(t, outSchema)
	data := outSchema["properties"].(map[string]any)["data"].(map[string]any)
	if data["type"] != "object" {
		t.Errorf("expected upstream outputSchema type preserved, got: %v", data)
	}
	props, _ := data["properties"].(map[string]any)
	if props == nil || props["temp"] == nil {
		t.Errorf("expected upstream outputSchema properties preserved, got: %v", data)
	}
}

func TestProxySchema_SyntheticSchemaIDScopedToServerAndTool(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "get_item",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "gh", conn)

	tool := findTool(toolsList(t, srv), "gh__get_item")
	args := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["args"].(map[string]any)
	if args["$id"] != "mini:schema:gh/get_item/input" {
		t.Errorf("expected synthetic $id scoped to server/tool, got: %v", args["$id"])
	}
}

func TestProxySchema_UpstreamAbsoluteSchemaIDPreserved(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "get_item",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object","$id":"https://example.com/schemas/get-item"}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "gh", conn)

	tool := findTool(toolsList(t, srv), "gh__get_item")
	args := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["args"].(map[string]any)
	if args["$id"] != "https://example.com/schemas/get-item" {
		t.Errorf("expected upstream $id to be preserved, got: %v", args["$id"])
	}
}

func TestProxySchema_RefsWithinArgsStayUnrewritten(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:        "get_item",
			Description: "desc",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"filter":{"$ref":"#/$defs/Filter"}},"$defs":{"Filter":{"type":"string"}}}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "gh", conn)

	tool := findTool(toolsList(t, srv), "gh__get_item")
	args := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["args"].(map[string]any)
	filterRef := args["properties"].(map[string]any)["filter"].(map[string]any)["$ref"]
	if filterRef != "#/$defs/Filter" {
		t.Errorf("expected $ref left untouched, got: %v", filterRef)
	}
	if args["$defs"] == nil {
		t.Errorf("expected $defs preserved alongside $ref, got: %v", args)
	}
}

func requireArgsRequired(t *testing.T, inSchema map[string]any) {
	t.Helper()
	req, ok := inSchema["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "args" {
		t.Errorf("expected required=[args], got: %v", inSchema["required"])
	}
}

func assertArgsSchema(t *testing.T, inSchema map[string]any, wantArgs map[string]any) {
	t.Helper()
	args, _ := inSchema["properties"].(map[string]any)["args"].(map[string]any)
	if args["type"] != wantArgs["type"] {
		t.Errorf("args.type = %v, want %v", args["type"], wantArgs["type"])
	}
	if args["$id"] == nil {
		t.Error("expected args schema to carry a synthesized $id")
	}
}

func assertStableOutputShape(t *testing.T, outSchema map[string]any) {
	t.Helper()
	if outSchema["type"] != "object" {
		t.Errorf("outputSchema.type = %v, want object", outSchema["type"])
	}
	required := toStringSlice(outSchema["required"])
	if len(required) != 1 || required[0] != "data" {
		t.Errorf("outputSchema.required = %v, want [data]", outSchema["required"])
	}
	props, _ := outSchema["properties"].(map[string]any)
	if props == nil || props["data"] == nil {
		t.Fatalf("outputSchema.properties.data missing: %v", outSchema)
	}
	mini, _ := props["__mini"].(map[string]any)
	if mini == nil {
		t.Fatalf("outputSchema.properties.__mini missing: %v", outSchema)
	}
	miniProps, _ := mini["properties"].(map[string]any)
	for _, key := range []string{"msg", "file", "excluded", "truncated", "passthrough"} {
		if miniProps[key] == nil {
			t.Errorf("outputSchema __mini.properties missing %q", key)
		}
	}
}

func toStringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, len(arr))
	for i, e := range arr {
		out[i], _ = e.(string)
	}
	return out
}

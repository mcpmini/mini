//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProxySchema_ArgsRequiredWhenUpstreamHasRequiredFields(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
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

func TestProxySchema_OutputSchema(t *testing.T) {
	cases := []struct {
		name     string
		upstream json.RawMessage
		want     string
	}{
		{"absent", nil, ``},
		{"null", json.RawMessage(`null`), ``},
		{
			"closed object gains __mini",
			json.RawMessage(`{"type":"object","properties":{"temp":{"type":"number"}},"additionalProperties":false}`),
			`{"type":"object","properties":{"temp":{"type":"number"},"__mini":MINI},"additionalProperties":false}`,
		},
		{
			"properties without type gains __mini",
			json.RawMessage(`{"properties":{"temp":{"type":"number"}}}`),
			`{"properties":{"temp":{"type":"number"},"__mini":MINI}}`,
		},
		{
			"object without properties gets them",
			json.RawMessage(`{"type":"object"}`),
			`{"type":"object","properties":{"__mini":MINI}}`,
		},
		{
			"array root unchanged",
			json.RawMessage(`{"type":"array","items":{"type":"string"}}`),
			`{"type":"array","items":{"type":"string"}}`,
		},
		{
			"upstream __mini property kept",
			json.RawMessage(`{"type":"object","properties":{"__mini":{"type":"string"}},"required":["__mini"]}`),
			`{"type":"object","properties":{"__mini":{"type":"string"}},"required":["__mini"]}`,
		},
		{"non-object root unchanged", json.RawMessage(`true`), `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			defer srv.Close()
			conn := &transport.FakeConnection{
				Tools: []transport.ToolDefinition{
					{Name: "tool", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: tc.upstream},
				},
				Responses: make(map[string]json.RawMessage),
			}
			addProxyConn(t, srv, "svc", conn)

			tool := findTool(toolsList(t, srv), "svc__tool")
			assertOutputSchema(t, tool, tc.want)
		})
	}
}

func assertOutputSchema(t *testing.T, tool map[string]any, want string) {
	t.Helper()
	got, present := tool["outputSchema"]
	if want == "" {
		if present {
			t.Fatalf("expected no outputSchema, got: %v", got)
		}
		return
	}
	maskMiniMetaSchema(t, got)
	var wantValue any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(want, "MINI", `"MINI"`)), &wantValue); err != nil {
		t.Fatal(err)
	}
	assertDeepEqual(t, got, wantValue)
}

func TestProxySchema_SyntheticSchemaIDScopedToServerAndTool(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
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

func TestProxySchema_NullUpstreamSchemaDoesNotPanic(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "null_input", Description: "desc", InputSchema: json.RawMessage(`null`)},
			{
				Name:         "null_output",
				Description:  "desc",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`null`),
			},
		},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(t, srv, "svc", conn)

	tools := toolsList(t, srv)
	if findTool(tools, "svc__null_input") == nil {
		t.Error("svc__null_input should appear in tools/list despite null inputSchema")
	}
	if findTool(tools, "svc__null_output") == nil {
		t.Error("svc__null_output should appear in tools/list despite null outputSchema")
	}
}

func TestProxySchema_RefsWithinArgsStayUnrewritten(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{
				Name:        "get_item",
				Description: "desc",
				InputSchema: json.RawMessage(
					`{"type":"object","properties":{"filter":{"$ref":"#/$defs/Filter"}},"$defs":{"Filter":{"type":"string"}}}`,
				),
			},
		},
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

func maskMiniMetaSchema(t *testing.T, schema any) {
	t.Helper()
	object, _ := schema.(map[string]any)
	props, _ := object["properties"].(map[string]any)
	meta, ok := props["__mini"].(map[string]any)
	if !ok {
		return
	}
	metaProps, ok := meta["properties"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"msg", "file", "excluded", "truncated", "passthrough"} {
		if metaProps[key] == nil {
			t.Errorf("__mini output schema missing %q", key)
		}
	}
	props["__mini"] = "MINI"
}

func toStringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, len(arr))
	for i, e := range arr {
		out[i], _ = e.(string)
	}
	return out
}

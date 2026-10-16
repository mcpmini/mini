//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

const miniInputJSON = `{"type":"object","properties":{"projection":{"type":"string","enum":["default","raw"]}},"additionalProperties":false}`

func TestProxySchema_InputSchema(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		want     string
	}{
		{
			"required fields gain __mini",
			`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`,
			`{"type":"object","properties":{"id":{"type":"string"},"__mini":MINI},"required":["id"]}`,
		},
		{
			"closed object gains __mini",
			`{"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false}`,
			`{"type":"object","properties":{"limit":{"type":"integer"},"__mini":MINI},"additionalProperties":false}`,
		},
		{
			"no type becomes object",
			`{"properties":{"limit":{"type":"integer"}}}`,
			`{"type":"object","properties":{"limit":{"type":"integer"},"__mini":MINI}}`,
		},
		{"object without properties gets them", `{"type":"object"}`, `{"type":"object","properties":{"__mini":MINI}}`},
		{
			"upstream __mini property is replaced",
			`{"type":"object","properties":{"__mini":{"type":"string"}}}`,
			`{"type":"object","properties":{"__mini":MINI}}`,
		},
		{
			"upstream $id kept",
			`{"type":"object","$id":"https://example.com/schemas/get-item"}`,
			`{"type":"object","$id":"https://example.com/schemas/get-item","properties":{"__mini":MINI}}`,
		},
		{
			"$ref in a property kept beside $defs",
			`{"type":"object","properties":{"filter":{"$ref":"#/$defs/Filter"}},"$defs":{"Filter":{"type":"string"}}}`,
			`{"type":"object","properties":{"filter":{"$ref":"#/$defs/Filter"},"__mini":MINI},"$defs":{"Filter":{"type":"string"}}}`,
		},
		{
			"closed $ref root unchanged",
			`{"$ref":"#/$defs/Input","$defs":{"Input":{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":false}}}`,
			`{"$ref":"#/$defs/Input","$defs":{"Input":{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":false}}}`,
		},
		{
			"oneOf root unchanged",
			`{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},{"type":"object","properties":{"name":{"type":"string"}}}]}`,
			`{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},{"type":"object","properties":{"name":{"type":"string"}}}]}`,
		},
		{
			"array root unchanged",
			`{"type":"array","items":{"type":"string"}}`,
			`{"type":"array","items":{"type":"string"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			defer srv.Close()
			conn := &transport.FakeConnection{
				Tools: []transport.ToolDefinition{
					{Name: "tool", Description: "desc", InputSchema: json.RawMessage(tc.upstream)},
				},
				Responses: make(map[string]json.RawMessage),
			}
			addProxyConn(t, srv, "svc", conn)

			tool := findTool(toolsList(t, srv), "svc__tool")
			if tool == nil {
				t.Fatal("svc__tool not found")
			}
			assertInputSchema(t, tool, tc.want)
		})
	}
}

func assertInputSchema(t *testing.T, tool map[string]any, want string) {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(want, "MINI", miniInputJSON)), &wantValue); err != nil {
		t.Fatal(err)
	}
	assertDeepEqual(t, tool["inputSchema"], wantValue)
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
	for _, key := range []string{"msg", "file", "excluded", "truncated"} {
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

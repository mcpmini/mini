//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolSchemas_PreserveNumbers(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "proxy", true: "detail"}[compact], func(t *testing.T) {
			srv := schemaNumberServer(t, json.RawMessage(`{"type":"object","const":9007199254740993,"maximum":1e400}`))
			tool := schemaToolResult(t, schemaResponse(t, srv, compact), compact)
			for _, key := range []string{"inputSchema", "outputSchema"} {
				schema := tool[key].(map[string]any)
				if !compact {
					field := map[string]string{"inputSchema": "args", "outputSchema": "data"}[key]
					schema = schema["properties"].(map[string]any)[field].(map[string]any)
				}
				if schema["const"] != json.Number("9007199254740993") || schema["maximum"] != json.Number("1e400") {
					t.Fatalf("%s numbers changed: %v", key, schema)
				}
			}
		})
	}
}

func TestToolSchemas_InvalidDefinitionReturnsError(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "proxy", true: "detail"}[compact], func(t *testing.T) {
			srv := schemaNumberServer(t, json.RawMessage(`{"type":`))
			decoder := json.NewDecoder(strings.NewReader(schemaResponse(t, srv, compact)))
			var response map[string]any
			if err := decoder.Decode(&response); err != nil {
				t.Fatal(err)
			}
			result, _ := response["result"].(map[string]any)
			if response["error"] == nil && result["isError"] != true {
				t.Fatalf("expected conversion failure, got %v", response)
			}
		})
	}
}

func schemaNumberServer(t *testing.T, schema json.RawMessage) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	conn := &transport.FakeConnection{Tools: []transport.ToolDefinition{{
		Name: "numbers", InputSchema: schema, OutputSchema: schema,
	}}}
	addProxyConn(t, srv, "svc", conn)
	return srv
}

func schemaResponse(t *testing.T, srv *server.Server, compact bool) string {
	t.Helper()
	request := rpc("tools/list", nil)
	if compact {
		request = callTool("list", map[string]any{"tool": "svc.numbers", "detail": true})
	}
	var out bytes.Buffer
	if err := srv.Serve(t.Context(), bytes.NewReader(buildServeInput(compact, [][]byte{request})), &out); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	return string(lines[len(lines)-1])
}

func schemaToolResult(t *testing.T, raw string, compact bool) map[string]any {
	t.Helper()
	response := decodeSchemaJSON(t, raw)
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("expected tool schemas, got %s", raw)
	}
	if compact {
		content := result["content"].([]any)[0].(map[string]any)
		return decodeSchemaJSON(t, content["text"].(string))
	}
	for _, tool := range result["tools"].([]any) {
		definition := tool.(map[string]any)
		if definition["name"] == "svc__numbers" {
			return definition
		}
	}
	t.Fatal("svc__numbers missing from discovery")
	return nil
}

func decodeSchemaJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

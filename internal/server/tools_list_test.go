//go:build test

package server_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProxy_ToolsList_ContainsMiniTools(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_issues", "create_issue")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "github", Connection: conn})

	tools := toolsList(t, srv)
	names := toolNames(tools)
	t.Logf("tools: %v", names)

	if !containsName(tools, "config") {
		t.Error("expected config in proxy tool list")
	}
	if !containsName(tools, "read") {
		t.Error("expected read in proxy tool list")
	}
	if !containsName(tools, "github__list_issues") {
		t.Error("expected github__list_issues in proxy tool list")
	}
	if !containsName(tools, "github__create_issue") {
		t.Error("expected github__create_issue in proxy tool list")
	}
}

func TestProxy_ToolsList_MiniToolsNoMeta(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	tools := toolsList(t, srv)
	for _, tool := range tools {
		name := tool["name"].(string)
		if name == "config" || name == "read" {
			if tool["_meta"] != nil {
				t.Errorf("%s: expected no _meta, got %v", name, tool["_meta"])
			}
		}
	}
}

func TestProxy_ToolsList_AnnotationsPassthrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	raw := json.RawMessage(`{"readOnlyHint":true}`)
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:         "get_file",
			Description:  "desc for get_file",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			Annotations:  raw,
			Title:        json.RawMessage(`"My Tool"`),
			OutputSchema: json.RawMessage(`{"type":"string"}`),
			Meta:         json.RawMessage(`{"key":"val"}`),
			Icons:        json.RawMessage(`{"url":"http://example.com/icon.png"}`),
			Execution:    json.RawMessage(`{"timeout":30}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__get_file")
	if tool == nil {
		t.Fatal("fs__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if tool[key] == nil {
			t.Errorf("expected %q in tool definition, got nil", key)
		}
	}
}

func TestProxy_ToolsList_MultiKeyAnnotationsPassthrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	raw := json.RawMessage(
		`{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false,"title":"Get File","fakeHint":true}`,
	)
	conn := fakeConnWithAnnotations("get_file", raw)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs2", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs2__get_file")
	if tool == nil {
		t.Fatal("fs2__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
}

func TestProxy_ToolsList_AbsentAnnotationsOmitted(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("write_file")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__write_file")
	if tool == nil {
		t.Fatal("fs__write_file not found in tools/list")
	}

	for _, key := range []string{"annotations", "title", "_meta", "icons", "execution"} {
		if _, ok := tool[key]; ok {
			t.Errorf("tool without %q must not include that key, got: %v", key, tool[key])
		}
	}
}

func TestProxy_ToolsList_OutputSchemaOmittedWhenUpstreamHasNone(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("write_file")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__write_file")
	if tool == nil {
		t.Fatal("fs__write_file not found in tools/list")
	}
	if _, ok := tool["outputSchema"]; ok {
		t.Errorf("tool without an upstream outputSchema must not advertise one, got: %v", tool["outputSchema"])
	}
}

func fakeConnWithAnnotations(name string, annotations json.RawMessage) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{
				Name:        name,
				Description: "desc for " + name,
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Annotations: annotations,
			},
		},
		Responses: make(map[string]json.RawMessage),
	}
}

func assertAnnotationsEqual(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got annotations: %v", err)
	}
	var gotVal, wantVal any
	if err := json.Unmarshal(gotRaw, &gotVal); err != nil {
		t.Fatalf("unmarshal got annotations: %v", err)
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("unmarshal want annotations: %v", err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("annotations mismatch: got %s, want %s", gotRaw, want)
	}
}

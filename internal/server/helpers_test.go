//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func rawServe(t *testing.T, srv *server.Server, input []byte) [][]byte {
	t.Helper()
	ctx := context.Background()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, bytes.NewReader(input), &out) }()
	<-done
	var lines [][]byte
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func toolsList(t *testing.T, srv *server.Server) []map[string]any {
	t.Helper()
	msgs := serveAll(t, srv, rpc("tools/list", nil))
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if tools, ok := res["tools"].([]any); ok {
				out := make([]map[string]any, len(tools))
				for i, tool := range tools {
					out[i] = tool.(map[string]any)
				}
				return out
			}
		}
	}
	t.Fatal("no tools/list result found")
	return nil
}

func parseRPCResponse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parse RPC response: %v\nraw: %s", err, data)
	}
	return resp
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t["name"].(string)
	}
	return names
}

func addEdgeConn(params addConnectionParams) {
	params.T.Helper()
	writeTestServerConfig(params.T, params.Server, params.Config)
	if err := params.Server.AddConnection(context.Background(), params.Config, params.Connection); err != nil {
		params.T.Fatalf("AddConnection %s: %v", params.Config.Name, err)
	}
}

func containsName(tools []map[string]any, name string) bool {
	for _, t := range tools {
		if t["name"] == name {
			return true
		}
	}
	return false
}

func assertRemoveOk(t *testing.T, srv *server.Server, serverName string) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": serverName,
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result: %s", text)
	}
	if result["error"] != nil {
		t.Errorf("expected ok=true, got: %v", result)
	}
}

type addConnectionParams struct {
	T          *testing.T
	Server     *server.Server
	Config     config.ServerConfig
	Connection *transport.FakeConnection
}

func assertIsErrorResult(t *testing.T, resp map[string]any) {
	t.Helper()
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true, got: %v", result)
	}
}

func findTool(tools []map[string]any, name string) map[string]any {
	for _, t := range tools {
		if t["name"] == name {
			return t
		}
	}
	return nil
}

func includeOnlyB(sessionOnly bool) map[string]any {
	return map[string]any{
		"action": "set_projection", "server": "svc", "tool": "getData", "session_only": sessionOnly,
		"projection": map[string]any{"include_only": []string{"b"}},
	}
}

func fakeConnWithError(name string) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: name, Description: name, InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":true}`),
		},
	}
}

func assertProjectedFields(params assertProjectedFieldsParams) {
	params.T.Helper()
	data := parseProxyEnvelope(params.T, params.Text).Data
	for _, key := range params.Present {
		if data[key] == nil {
			params.T.Errorf("field %q missing from projected data %v", key, data)
		}
	}
	for _, key := range params.Absent {
		if data[key] != nil {
			params.T.Errorf("field %q survived projection in %v", key, data)
		}
	}
}

type assertProjectedFieldsParams struct {
	T       *testing.T
	Text    string
	Present []string
	Absent  []string
}

//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
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
	removing := inBackground(srv, newTestSession(t, removeServerCall(serverName)))
	output := testutil.WaitForReceive(t, removing.output, "remove_server to return")
	assertConfigOk(t, removing.session.response(t, output))
}

func removeServerCall(serverName string) []byte {
	return callTool("config", map[string]any{"action": "remove_server", "server": serverName})
}

func assertConfigOk(t *testing.T, resp map[string]any) {
	t.Helper()
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

func setGetDataProjection(id int, sessionOnly bool) map[string]any {
	return map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "config", "arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData", "session_only": sessionOnly,
				"projection": map[string]any{"include_only": []string{"b"}},
			},
		},
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

type heldStep struct {
	t       *testing.T
	reached chan struct{}
	gate    chan struct{}
	arrive  func()
	release func()
}

func newHeldStep(t *testing.T) *heldStep {
	h := &heldStep{t: t, reached: make(chan struct{}), gate: make(chan struct{})}
	h.arrive = sync.OnceFunc(func() { close(h.reached) })
	h.release = sync.OnceFunc(func() { close(h.gate) })
	t.Cleanup(h.release)
	return h
}

func (h *heldStep) hold() {
	h.arrive()
	select {
	case <-h.gate:
	case <-h.t.Context().Done(): // a failing test must not hang in a server's Close on a held handler
	}
}

func (h *heldStep) awaitReached(what string) {
	h.t.Helper()
	testutil.WaitForReceive(h.t, h.reached, what)
}

func assertNoFile(t *testing.T, what, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: stat = %v, want no file", what, err)
	}
}

var svcLifecycleChanges = []struct {
	name        string
	change      func(*testing.T, *server.Server)
	servesAfter []string
}{
	{
		name:   "removed",
		change: func(t *testing.T, srv *server.Server) { assertRemoveOk(t, srv, "svc") },
	},
	{
		name: "replaced",
		change: func(t *testing.T, srv *server.Server) {
			if err := srv.AddConnection(
				context.Background(),
				config.ServerConfig{Name: "svc"},
				fakeConn("replacement"),
			); err != nil {
				t.Fatal(err)
			}
		},
		servesAfter: []string{"svc__replacement"},
	},
}

func assertSvcServes(t *testing.T, srv *server.Server, want ...string) {
	t.Helper()
	var got []string
	for _, name := range toolNames(toolsList(t, srv)) {
		if strings.HasPrefix(name, "svc__") {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("svc serves %v, want %v", got, want)
	}
}

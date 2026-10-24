//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

// verifyEnvelope checks the envelope structure returned by execute.
func verifyEnvelope(t *testing.T, text string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("execute result is not JSON: %v\n%s", err, text)
	}
	return env
}

func newPipelineServer(t *testing.T, toolName string, response string) (*server.Server, *transport.FakeConnection) {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: toolName, Description: toolName, InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(response)},
	}
	return srv, fake
}

func TestExecuteReturnsEnvelope(t *testing.T) {
	srv, fake := newPipelineServer(t, "getBuild",
		`{"content":[{"type":"text","text":"{\"build_number\":1234,\"status\":\"failed\",\"branch\":\"main\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "ci"}, fake)
	resp := serve(t, srv, callTool("ci__getBuild", map[string]any{"id": "1234"}))
	verifyEnvelope(t, successText(t, resp))
}

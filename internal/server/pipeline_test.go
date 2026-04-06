//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
	if env["ok"] == nil {
		t.Error("envelope missing 'ok' field")
	}
	return env
}

func newPipelineServer(t *testing.T, toolName string, response string) (*server.Server, *transport.FakeConnection) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: toolName, Description: toolName, InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(response)},
	}
	return srv, fake
}

func TestExecuteReturnsEnvelope(t *testing.T) {
	srv, fake := newPipelineServer(t, "getBuild",
		`{"content":[{"type":"text","text":"{\"build_number\":1234,\"status\":\"failed\",\"branch\":\"main\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "ci"}, fake)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "ci", "tool": "getBuild", "params": map[string]any{"id": "1234"},
	}))
	env := verifyEnvelope(t, toolResultText(t, resp))
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env["ok"])
	}
}

func TestExecuteInlineForSmallResponse(t *testing.T) {
	srv, fake := newPipelineServer(t, "ping", `{"content":[{"type":"text","text":"{\"pong\":true}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	var env map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &env) //nolint:errcheck
	if env["file"] != nil {
		t.Error("expected no file for small inline response")
	}
}

func TestSessionProjectionOverride(t *testing.T) {
	srv, fake := newPipelineServer(t, "getBuild",
		`{"content":[{"type":"text","text":"{\"build_number\":1,\"status\":\"ok\",\"secret\":\"topsecret\",\"branch\":\"main\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "ci"}, fake)
	// Set a session projection that excludes "secret"; validates configure doesn't error.
	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "ci",
		"tool":       "getBuild",
		"projection": map[string]any{"include": []string{"build_number", "status", "branch"}},
	}))
}

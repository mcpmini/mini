//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/usage"
)

func newTestServerWithUsage(t *testing.T) (*server.Server, string) {
	t.Helper()
	usagePath := filepath.Join(t.TempDir(), "usage.json")
	srv := newTestServerAt(t, usagePath)
	return srv, usagePath
}

func newTestServerAt(t *testing.T, usagePath string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(cfg, logger, server.WithUsagePath(usagePath))
}

func TestUsage_successCallIsRecorded(t *testing.T) {
	srv, usagePath := newTestServerWithUsage(t)

	fake := fakeConn("get_issue")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"issue data"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "jira"}, fake)

	serve(t, srv, callTool("call", map[string]any{
		"server": "jira", "tool": "get_issue", "params": map[string]any{},
	}))

	srv.Close()

	tr := usage.New(usagePath)
	if err := tr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	top := tr.TopTools(0)
	if len(top) != 1 {
		t.Fatalf("expected 1 tracked tool, got %d", len(top))
	}
	if top[0].Tool != "get_issue" || top[0].Server != "jira" {
		t.Errorf("unexpected entry: %+v", top[0])
	}
	if top[0].Calls != 1 {
		t.Errorf("expected 1 call, got %d", top[0].Calls)
	}
	if top[0].Errors != 0 {
		t.Errorf("expected 0 errors, got %d", top[0].Errors)
	}
}

func TestUsage_errorCallIsRecorded(t *testing.T) {
	srv, usagePath := newTestServerWithUsage(t)

	fake := fakeConn("bad_tool")
	fake.Responses["tools/call"] = json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "svc"}, fake)

	serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "bad_tool", "params": map[string]any{},
	}))

	srv.Close()

	tr := usage.New(usagePath)
	if err := tr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	top := tr.TopTools(0)
	if len(top) != 1 {
		t.Fatalf("expected 1 tracked tool, got %d: %v", len(top), top)
	}
	if top[0].Errors != 1 {
		t.Errorf("expected 1 error, got %d", top[0].Errors)
	}
}

func TestUsage_surfacedInStatus(t *testing.T) {
	srv, _ := newTestServerWithUsage(t)

	fake := fakeConn("search_code")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"results"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "gh"}, fake)

	for range 3 {
		serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "search_code", "params": map[string]any{},
		}))
	}

	resp := serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	lu, ok := status["local_usage"]
	if !ok {
		t.Fatal("local_usage missing from status response")
	}
	tools, ok := lu.([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty local_usage slice, got: %v", lu)
	}
}

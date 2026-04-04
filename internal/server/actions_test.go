package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

const actionYAML = `name: my_search
description: Search with defaults
server: gh
tool: search_code
default_args:
  language: go
  per_page: 10
`

func writeActionYAML(t *testing.T, dir string) {
	t.Helper()
	actionsDir := filepath.Join(dir, "actions")
	os.MkdirAll(actionsDir, 0700)
	os.WriteFile(filepath.Join(actionsDir, "my_search.yaml"), []byte(actionYAML), 0600)
}

func serverWithActionsDir(t *testing.T, dir string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fakeGHConn() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "search_code", Description: "search code", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"[]"}]}`),
		},
	}
}

func TestLoadActions_loadsFromDir(t *testing.T) {
	dir := t.TempDir()
	writeActionYAML(t, dir)
	srv := serverWithActionsDir(t, dir)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, fakeGHConn())

	if err := srv.LoadActions(dir); err != nil {
		t.Fatalf("LoadActions failed: %v", err)
	}

	resp := serve(t, srv, callTool("list", map[string]any{"query": "my_search"}))
	text := toolResultText(t, resp)
	var results []map[string]any
	json.Unmarshal([]byte(text), &results)
	if len(results) == 0 {
		t.Errorf("expected action to appear in discover: %s", text)
	}
}

func TestLoadActions_emptyDir(t *testing.T) {
	srv := serverWithActionsDir(t, t.TempDir())
	if err := srv.LoadActions(t.TempDir()); err != nil {
		t.Errorf("LoadActions on empty dir should not error: %v", err)
	}
}

func TestLoadActions_invalidYAML(t *testing.T) {
	dir := t.TempDir()
	actionsDir := filepath.Join(dir, "actions")
	os.MkdirAll(actionsDir, 0700)
	os.WriteFile(filepath.Join(actionsDir, "bad.yaml"), []byte(":\t invalid"), 0600)

	srv := serverWithActionsDir(t, dir)
	if err := srv.LoadActions(dir); err == nil {
		t.Error("expected error for invalid YAML action file")
	}
}

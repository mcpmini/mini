//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newSrvWithFormat(t *testing.T, format config.ResponseFormat) *server.Server {
	return newSrvWithResponse(
		t,
		format,
		`[{"number":1,"title":"bug one","state":"open"},{"number":2,"title":"feat two","state":"closed"}]`,
	)
}

func newSrvWithResponse(t *testing.T, format config.ResponseFormat, payload string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = format
	srv := newTestServer(t, server.Params{Config: cfg})

	issuesJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "list_issues", Description: "list", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(issuesJSON) + `}]}`),
		},
	}
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "gh"}, Connection: fake})
	return srv
}

func TestToonFormatRendersTabularBlock(t *testing.T) {
	srv := newSrvWithFormat(t, "toon")
	resp := serve(t, srv, callTool("gh__list_issues", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format, got JSON: %s", text)
	}
	if !strings.HasPrefix(text, "[2]{number,title,state}:") {
		t.Fatalf("expected a TOON tabular header for data, got: %s", text)
	}
	for _, want := range []string{"1,", "bug one", "2,", "feat two"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in TOON output, got: %s", want, text)
		}
	}
}

func TestToonFormatOmitsInternalTokenStats(t *testing.T) {
	srv := newSrvWithFormat(t, "toon")

	resp := serve(t, srv, callTool("gh__list_issues", map[string]any{}))

	text := toolResultText(t, resp)
	if strings.Contains(text, "tokens:") {
		t.Errorf("token stats should not appear in agent-facing output: %s", text)
	}
}

func TestJSONFormatDefault(t *testing.T) {
	srv := newSrvWithFormat(t, "json")

	resp := serve(t, srv, callTool("gh__list_issues", map[string]any{}))

	text := toolResultText(t, resp)
	var env []any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope, got: %s", text)
	}
}

func newSrvWithConfigFormat(t *testing.T, format config.ResponseFormat) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = format
	issues := `[{"number":1,"title":"bug"},{"number":2,"title":"feat"}]`
	issuesJSON, _ := json.Marshal(issues)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "list_issues", Description: "list", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(issuesJSON) + `}]}`),
		},
	}
	srv := newTestServer(t, server.Params{Config: cfg})
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "gh"}, Connection: fake})
	return srv
}

func TestToonFormatPerToolOverride(t *testing.T) {
	srv := newSrvWithConfigFormat(t, "json")
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_issues",
		"projection": map[string]any{"format": "toon"},
	}))
	resp := serve(t, srv, callTool("gh__list_issues", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Errorf("expected TOON format (per-tool override), got JSON: %s", text)
	}
	if !strings.HasPrefix(text, "[2]{number,title}:") {
		t.Errorf("expected TOON tabular block for per-tool override: %s", text)
	}
}

func TestToonFormatWithUpstream(t *testing.T) {
	fake := fakeConn("list_directory")
	fake.RespondWith(map[string]any{"entries": []string{"a.txt", "b.txt", "c.go"}})
	srv := newTestServer(t, server.Params{})
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "fs"}, Connection: fake})

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "list_directory",
		"projection": map[string]any{"format": "toon"},
	}))
	resp := serve(t, srv, callTool("fs__list_directory", map[string]any{"path": "/dir"}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format, got JSON: %s", text)
	}
	if !strings.HasPrefix(text, "entries") {
		t.Errorf("expected TOON output to start with the data field, got: %s", text)
	}
}

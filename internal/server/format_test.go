//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newSrvWithFormat(t *testing.T, format string) *server.Server {
	return newSrvWithResponse(t, format, 10000, `[{"number":1,"title":"bug one","state":"open"},{"number":2,"title":"feat two","state":"closed"}]`)
}

func newSrvWithResponse(t *testing.T, format string, inlineThreshold int, payload string) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = inlineThreshold
	cfg.ResponseFormat = format
	srv := server.New(cfg, logger)

	issuesJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "list_issues", Description: "list", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(issuesJSON) + `}]}`),
		},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, fake)
	return srv
}

func TestLinesFormatRendersOneLinePerItem(t *testing.T) {
	srv := newSrvWithFormat(t, "lines")
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected lines format, got JSON: %s", text)
	}
	if !strings.Contains(text, "[gh.list_issues]") {
		t.Errorf("expected header line, got: %s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines, got %d:\n%s", len(lines), text)
	}
	if !strings.Contains(lines[1], "number") && !strings.Contains(lines[1], "title") {
		t.Errorf("expected column header row, got: %s", lines[1])
	}
}

func TestLinesFormatHasHeader(t *testing.T) {
	srv := newSrvWithFormat(t, "lines")

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	// Header should be present, no token stats (those are internal)
	if !strings.Contains(text, "[gh.list_issues]") {
		t.Errorf("expected [gh.list_issues] header, got: %s", text)
	}
	if strings.Contains(text, "tokens:") {
		t.Errorf("token stats should not appear in agent-facing output: %s", text)
	}
}

func TestJSONFormatDefault(t *testing.T) {
	srv := newSrvWithFormat(t, "json")

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope, got: %s", text)
	}
}

func newSrvWithConfigFormat(t *testing.T, format string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.ResponseFormat = format
	issues := `[{"number":1,"title":"bug"},{"number":2,"title":"feat"}]`
	issuesJSON, _ := json.Marshal(issues)
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "list_issues", Description: "list", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(issuesJSON) + `}]}`)},
	}
	srv := server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, fake)
	return srv
}

func TestLinesFormatPerToolOverride(t *testing.T) {
	srv := newSrvWithConfigFormat(t, "json")
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_issues",
		"projection": map[string]any{"format": "lines"},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Errorf("expected lines format (per-tool override), got JSON: %s", text)
	}
	if !strings.Contains(text, "[gh.list_issues]") {
		t.Errorf("expected header line in per-tool lines format: %s", text)
	}
}

func TestLinesFormatIncludesSpilledFilePath(t *testing.T) {
	payload := `{"items":[{"number":1,"title":"` + strings.Repeat("x", 400) + `"},{"number":2,"title":"` + strings.Repeat("y", 400) + `"}]}`
	lines := spilledLinesResponse(t, payload)
	assertSpilledLinesFormat(t, lines)
}

func spilledLinesResponse(t *testing.T, payload string) []string {
	t.Helper()
	srv := newSrvWithResponse(t, "lines", 1, payload)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected lines format, got JSON: %s", text)
	}
	return strings.Split(strings.TrimSpace(text), "\n")
}

func assertSpilledLinesFormat(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) != 2 {
		t.Fatalf("expected header and placeholder line, got %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "[gh.list_issues] file:") {
		t.Fatalf("expected file header, got: %s", lines[0])
	}
	assertSpilledLinesFile(t, lines[0])
	if lines[1] != "null" {
		t.Fatalf("expected placeholder line for spilled response, got %q", lines[1])
	}
}

func assertSpilledLinesFile(t *testing.T, header string) {
	t.Helper()
	path := strings.TrimPrefix(header, "[gh.list_issues] file:")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected spilled response file to exist: %v", err)
	}
}

func fetchServerStatus(t *testing.T, srv *server.Server, serverName string) map[string]any {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, text)
	}
	servers, _ := status["servers"].(map[string]any)
	svc, _ := servers[serverName].(map[string]any)
	if svc == nil {
		t.Fatalf("%s not in status: %s", serverName, text)
	}
	return svc
}

func TestHealthStatsInConfigureStatus(t *testing.T) {
	srv := newTestServer(t)
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "ping", "params": map[string]any{}}))
	svc := fetchServerStatus(t, srv, "svc")
	if svc["calls"] == nil {
		t.Errorf("expected calls in server stats: %v", svc)
	}
	if svc["status"] == nil {
		t.Errorf("expected status in server stats: %v", svc)
	}
	if svc["tools"] == nil {
		t.Errorf("expected tools count in server stats: %v", svc)
	}
}

func TestConnErrorTriggersReconnect(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	fake.Err = errors.New("simulated connection failure")
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	env := parseEnvelope(t, text)
	if env["ok"] != false {
		t.Errorf("expected ok=false on connection error: %s", text)
	}
}

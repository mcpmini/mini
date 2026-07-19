//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newSrvWithFormat(t *testing.T, format string) *server.Server {
	return newSrvWithResponse(t, format, `[{"number":1,"title":"bug one","state":"open"},{"number":2,"title":"feat two","state":"closed"}]`)
}

func newSrvWithResponse(t *testing.T, format string, payload string) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
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

func TestToonFormatRendersTabularBlock(t *testing.T) {
	srv := newSrvWithFormat(t, "toon")
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format, got JSON: %s", text)
	}
	// Envelope data passes through Go maps (sorted key order on marshal), so the
	// tabular header is alphabetical, not source JSON order.
	if !strings.HasPrefix(text, "data[2]{number,state,title}:") {
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

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))

	text := toolResultText(t, resp)
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

func TestToonFormatPerToolOverride(t *testing.T) {
	srv := newSrvWithConfigFormat(t, "json")
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_issues",
		"projection": map[string]any{"format": "toon"},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Errorf("expected TOON format (per-tool override), got JSON: %s", text)
	}
	if !strings.HasPrefix(text, "data[2]{number,title}:") {
		t.Errorf("expected TOON tabular block for per-tool override: %s", text)
	}
}

func TestToonFormatIncludesFileFieldWhenElisionOccurs(t *testing.T) {
	payload := `{"items":[{"number":1,"title":"bug one"},{"number":2,"title":"feat two"}],"secret":"hidden"}`
	text := elisionToonResponse(t, payload)
	assertElisionToonFormat(t, text)
}

func elisionToonResponse(t *testing.T, payload string) string {
	t.Helper()
	srv := newSrvWithResponse(t, "toon", payload)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_issues",
		"projection": map[string]any{"format": "toon", "exclude": []string{"secret"}},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format, got JSON: %s", text)
	}
	return text
}

func assertElisionToonFormat(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, "hidden") {
		t.Errorf("excluded field value must not appear in TOON output: %s", text)
	}
	key := extractToonFileKey(t, text)
	if strings.ContainsAny(key, "/\\") || strings.HasSuffix(key, ".json") || len(key) < 13 {
		t.Fatalf("expected bare recovery key in file field (no path, no extension), got: %s", key)
	}
}

func extractToonFileKey(t *testing.T, text string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^file: (\S+)$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("expected a file field in TOON output, got: %s", text)
	}
	// Recovery keys are all-digit unix_ms timestamps, so TOON's numeric-like
	// quoting rule (spec §7.2) always wraps them in double quotes.
	return strings.Trim(m[1], `"`)
}

func TestReadTool_ResolvesFileWrittenByCompactModeToonFormat(t *testing.T) {
	payload := `{"items":[{"id":1,"title":"bug"}],"secret":"hidden"}`
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.ResponseFormat = "toon"
	srv := server.New(cfg, logger)
	defer srv.Close()

	conn := fakeConn("list_issues")
	payloadJSON, _ := json.Marshal(payload)
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`)
	addProxyConn(t, srv, "gh", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_issues",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_issues", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	key := extractToonFileKey(t, text)

	resp2 := serveProxy(t, srv, callTool("read", map[string]any{"file": key}))
	content := toolResultText(t, resp2)
	if content == "" {
		t.Fatal("read returned empty content for bare key from TOON format")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Errorf("read via TOON-format bare key should return valid JSON: %s", content)
	}
}

func toolErrFakeConn() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "list_issues", Description: "list", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"upstream failed"}],"isError":true}`),
		},
	}
}

func newSrvConfigDirAndToolErr(t *testing.T, globalFormat string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.ResponseFormat = globalFormat
	srv := server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, toolErrFakeConn())
	return srv
}

// TOON objects never start with "{" — the leading character is always a field name.
func assertToonFormat(t *testing.T, text string) {
	t.Helper()
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format, got JSON: %.200s", text)
	}
	if !strings.Contains(text, "error:") {
		t.Errorf("expected error field in TOON output: %s", text)
	}
}

// Notifications and non-result messages are skipped; the last tool content wins.
func lastToolResult(t *testing.T, msgs []map[string]any) string {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		result, ok := msgs[i]["result"].(map[string]any)
		if !ok {
			continue
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		text, ok := content[0].(map[string]any)["text"].(string)
		if !ok {
			continue
		}
		return text
	}
	t.Fatal("no tool result text found in messages")
	return ""
}

func TestErrorEnvelopeHonorsFormat(t *testing.T) {
	t.Run("global toon renders tool_error as TOON", func(t *testing.T) {
		srv := newSrvConfigDirAndToolErr(t, "toon")
		text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "list_issues", "params": map[string]any{},
		})))
		assertToonFormat(t, text)
	})

	t.Run("exact-tool toon projection renders tool_error as TOON", func(t *testing.T) {
		srv := newSrvConfigDirAndToolErr(t, "")
		serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "gh", "tool": "list_issues",
			"projection": map[string]any{"format": "toon"},
		}))
		text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "list_issues", "params": map[string]any{},
		})))
		assertToonFormat(t, text)
	})

	t.Run("wildcard toon projection renders tool_error as TOON", func(t *testing.T) {
		configDir := t.TempDir()
		// loadServerProjections only merges projections for servers with a .yaml config file.
		os.MkdirAll(filepath.Join(configDir, "servers"), 0755) //nolint:errcheck
		os.WriteFile(filepath.Join(configDir, "servers", "gh.yaml"), //nolint:errcheck
			[]byte("name: gh\ncommand: unused\n"), 0644)
		os.WriteFile(filepath.Join(configDir, "servers", "gh.proj.yaml"), //nolint:errcheck
			[]byte("\"*\":\n  format: toon\n"), 0644)
		cfg := config.DefaultConfig()
		cfg.ResponseDir = t.TempDir()
		srv := server.NewWithConfigDir(cfg, configDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, toolErrFakeConn())
		text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "list_issues", "params": map[string]any{},
		})))
		assertToonFormat(t, text)
	})

	t.Run("session-override toon projection renders tool_error as TOON", func(t *testing.T) {
		srv := newSrvConfigDirAndToolErr(t, "")
		msgs := serveAll(t, srv,
			callTool("config", map[string]any{
				"action": "set_projection", "server": "gh", "tool": "list_issues",
				"projection": map[string]any{"format": "toon"}, "session_only": true,
			}),
			callTool("call", map[string]any{
				"server": "gh", "tool": "list_issues", "params": map[string]any{},
			}),
		)
		assertToonFormat(t, lastToolResult(t, msgs))
	})

	t.Run("not_found under global toon renders as TOON", func(t *testing.T) {
		srv := newSrvConfigDirAndToolErr(t, "toon")
		text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "nonexistent_tool", "params": map[string]any{},
		})))
		if strings.HasPrefix(text, "{") {
			t.Fatalf("expected TOON for not_found under global toon, got JSON: %.200s", text)
		}
		if !strings.Contains(text, "error:") {
			t.Errorf("expected error field in TOON not_found output: %s", text)
		}
	})

	t.Run("per-tool json projection overrides global toon for tool_error", func(t *testing.T) {
		srv := newSrvConfigDirAndToolErr(t, "toon")
		serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "gh", "tool": "list_issues",
			"projection": map[string]any{"format": "json"},
		}))
		text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "gh", "tool": "list_issues", "params": map[string]any{},
		})))
		if !strings.HasPrefix(text, "{") {
			t.Fatalf("expected JSON for tool_error when per-tool json overrides global toon, got: %.200s", text)
		}
	})
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
	if env["error"] == nil {
		t.Errorf("expected ok=false on connection error: %s", text)
	}
}

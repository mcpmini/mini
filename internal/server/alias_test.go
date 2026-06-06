//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

// lastCalledTool extracts the tool name from FakeConnection's LastParams,
// which holds the marshaled tools/call payload: {"name":"<tool>","arguments":{...}}.
func lastCalledTool(t *testing.T, fake *transport.FakeConnection) string {
	t.Helper()
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(fake.LastParams, &p); err != nil {
		t.Fatalf("unmarshal LastParams: %v", err)
	}
	return p.Name
}

// TestAlias_listShowsAliasName verifies that when a projection config declares
// an alias, the agent sees the alias in list output instead of the real tool name.
func TestAlias_listShowsAliasName(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("list_pull_requests", "get_issue")
	proj := map[string]*config.ProjectionConfig{
		"list_pull_requests": {Alias: "list_prs"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Projections: proj}, fake)

	results := mustDiscoverResults(t, srv, map[string]any{})
	names := map[string]bool{}
	for _, e := range results {
		names[e["name"].(string)] = true
	}

	if names["gh.list_pull_requests"] {
		t.Error("real name should not appear in list when aliased")
	}
	if !names["gh.list_prs"] {
		t.Error("alias should appear in list")
	}
	if !names["gh.get_issue"] {
		t.Error("non-aliased tool should still appear under real name")
	}
}

// TestAlias_callRoutesToRealTool verifies that calling via alias forwards the
// real tool name to the upstream.
func TestAlias_callRoutesToRealTool(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("list_pull_requests")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[]"}]}`)
	proj := map[string]*config.ProjectionConfig{
		"list_pull_requests": {Alias: "list_prs"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh",
		"tool":   "list_prs",
		"params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	if text == "" {
		t.Fatal("expected non-empty response")
	}

	// Verify the upstream received the real tool name.
	if got := lastCalledTool(t, fake); got != "list_pull_requests" {
		t.Errorf("upstream should have received real tool name, got %q", got)
	}
}

// TestAlias_callByRealNameFails verifies that calling by the real name fails
// when an alias is configured (the real name is no longer in the registry).
func TestAlias_callByRealNameFails(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("list_pull_requests")
	proj := map[string]*config.ProjectionConfig{
		"list_pull_requests": {Alias: "list_prs"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh",
		"tool":   "list_pull_requests",
		"params": map[string]any{},
	}))

	env, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result envelope")
	}
	if env["ok"] == true {
		t.Error("calling by real name when aliased should fail")
	}
}

// TestAlias_permCallRoutesToRealTool verifies that perm_call also routes through
// alias resolution correctly.
func TestAlias_permCallRoutesToRealTool(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("delete_repo")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"deleted"}]}`)
	perm := &config.PermissionsConfig{Protected: []string{"delete_repo"}}
	proj := map[string]*config.ProjectionConfig{
		"delete_repo": {Alias: "rm_repo"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Permissions: perm, Projections: proj}, fake)

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "gh",
		"tool":   "rm_repo",
		"params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	if text == "" {
		t.Fatal("expected non-empty response from perm_call via alias")
	}
	if got := lastCalledTool(t, fake); got != "delete_repo" {
		t.Errorf("upstream should have received real tool name, got %q", got)
	}
}

// TestAlias_invalidAliasUsesRealName verifies that an invalid alias (containing
// characters outside [a-zA-Z0-9_-]) is silently ignored and the real name is used.
func TestAlias_invalidAliasUsesRealName(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("my_tool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	proj := map[string]*config.ProjectionConfig{
		"my_tool": {Alias: "bad alias!"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "svc", Projections: proj}, fake)

	results := mustDiscoverResults(t, srv, map[string]any{})
	names := map[string]bool{}
	for _, e := range results {
		names[e["name"].(string)] = true
	}
	if !names["svc.my_tool"] {
		t.Error("tool should appear under real name when alias is invalid")
	}
}

// TestAlias_noProjectionAlias verifies normal (non-aliased) tools are unaffected.
func TestAlias_noProjectionAlias(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("get_issue")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"issue"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "gh"}, fake)

	results := mustDiscoverResults(t, srv, map[string]any{})
	names := map[string]bool{}
	for _, e := range results {
		names[e["name"].(string)] = true
	}
	if !names["gh.get_issue"] {
		t.Errorf("non-aliased tool should appear under real name, got: %v", results)
	}
}

func newAliasConfigServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	return srv
}

func TestAlias_sessionSetProjectionStoresUnderRealName(t *testing.T) {
	srv := newAliasConfigServer(t)

	fake := fakeConn("list_prs_real")
	proj := map[string]*config.ProjectionConfig{"list_prs_real": {Alias: "list_prs"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "list_prs",
		"projection":   map[string]any{"exclude_always": []string{"secret"}},
		"session_only": true,
	}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "error") {
		t.Fatalf("set_projection failed: %s", text)
	}
	if !strings.Contains(text, `"ok":true`) {
		t.Errorf("expected ok=true from set_projection, got: %s", text)
	}
}

func TestAlias_serverSetProjectionTakesEffect(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	payload := `{"id":1,"secret":"hidden","name":"foo"}`
	payloadJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "get_pr_real", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`),
		},
	}
	proj := map[string]*config.ProjectionConfig{"get_pr_real": {Alias: "get_pr"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, fake)

	configResp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "get_pr",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))
	configText := toolResultText(t, configResp)
	if strings.Contains(configText, "error") {
		t.Fatalf("set_projection failed: %s", configText)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "get_pr", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "hidden") {
		t.Errorf("server projection for alias should exclude secret field; got: %s", text)
	}
}

func TestAlias_setProjectionResponseEchoesAlias(t *testing.T) {
	srv := newAliasConfigServer(t)

	fake := fakeConn("real_tool")
	proj := map[string]*config.ProjectionConfig{"real_tool": {Alias: "aliased_tool"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", Projections: proj}, fake)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "aliased_tool",
		"projection":   map[string]any{"exclude_always": []string{"x"}},
		"session_only": true,
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "aliased_tool") {
		t.Errorf("set_projection response should echo alias name, got: %s", text)
	}
	if strings.Contains(text, "real_tool") {
		t.Errorf("set_projection response should not expose real tool name, got: %s", text)
	}
}

func TestAlias_reloadUpdatesAliases(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	fake := fakeConn("list_pull_requests")
	proj := map[string]*config.ProjectionConfig{"list_pull_requests": {Alias: "list_prs"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, fake)

	serve(t, srv, callTool("config", map[string]any{"action": "reload"}))

	results := mustDiscoverResults(t, srv, map[string]any{})
	names := map[string]bool{}
	for _, e := range results {
		names[e["name"].(string)] = true
	}
	if !names["gh.list_prs"] {
		t.Errorf("alias should still appear after reload, got: %v", results)
	}
	if names["gh.list_pull_requests"] {
		t.Errorf("real name should not appear when aliased, got: %v", results)
	}
}

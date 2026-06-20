//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

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

func listNames(t *testing.T, srv *server.Server) map[string]bool {
	t.Helper()
	results := mustDiscoverResults(t, srv, map[string]any{})
	names := map[string]bool{}
	for _, e := range results {
		names[e["name"].(string)] = true
	}
	return names
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAlias_listShowsAliasName(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("list_pull_requests", "get_issue")
	proj := map[string]*config.ProjectionConfig{
		"list_pull_requests": {Alias: "list_prs"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Projections: proj}, fake)

	names := listNames(t, srv)
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

	if text := toolResultText(t, resp); text == "" {
		t.Fatal("expected non-empty response")
	}
	if got := lastCalledTool(t, fake); got != "list_pull_requests" {
		t.Errorf("upstream should have received real tool name, got %q", got)
	}
}

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

	if text := toolResultText(t, resp); text == "" {
		t.Fatal("expected non-empty response from perm_call via alias")
	}
	if got := lastCalledTool(t, fake); got != "delete_repo" {
		t.Errorf("upstream should have received real tool name, got %q", got)
	}
}

func TestAlias_invalidAliasUsesRealName(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("my_tool")
	proj := map[string]*config.ProjectionConfig{
		"my_tool": {Alias: "bad alias!"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "svc", Projections: proj}, fake)

	names := listNames(t, srv)
	if !names["svc.my_tool"] {
		t.Error("tool should appear under real name when alias is invalid")
	}
}

func TestAlias_noProjectionAlias(t *testing.T) {
	srv := newTestServer(t)

	fake := fakeConn("get_issue")
	addTestConnection(t, srv, config.ServerConfig{Name: "gh"}, fake)

	names := listNames(t, srv)
	if !names["gh.get_issue"] {
		t.Errorf("non-aliased tool should appear under real name, got: %v", names)
	}
}

func newAliasConfigServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
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
	if configText := toolResultText(t, configResp); strings.Contains(configText, "error") {
		t.Fatalf("set_projection failed: %s", configText)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "get_pr", "params": map[string]any{},
	}))
	if text := toolResultText(t, resp); strings.Contains(text, "hidden") {
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
	tests := []struct {
		name              string
		initialProjection map[string]*config.ProjectionConfig
		wantGoneAfter     string
	}{
		{
			name:              "server had no inline projections at startup",
			initialProjection: nil,
			wantGoneAfter:     "gh.list_pull_requests",
		},
		{
			name:              "server had an inline projection alias at startup",
			initialProjection: map[string]*config.ProjectionConfig{"list_pull_requests": {Alias: "old_alias"}},
			wantGoneAfter:     "gh.old_alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
			t.Cleanup(srv.Close)

			// Server stub lets loadServerProjections merge projection files for "gh".
			writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), "name: gh\n")

			fake := fakeConn("list_pull_requests")
			srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: tt.initialProjection}, fake)

			// Write disk projection with a new alias and reload — reapplyAliases must pick it up.
			writeFile(t, filepath.Join(dir, "projections", "gh.yaml"), "list_pull_requests:\n  alias: list_prs\n")
			serve(t, srv, callTool("config", map[string]any{"action": "reload"}))

			names := listNames(t, srv)
			if !names["gh.list_prs"] {
				t.Errorf("alias should appear after reload, got: %v", names)
			}
			if names[tt.wantGoneAfter] {
				t.Errorf("%s should not appear after reload, got: %v", tt.wantGoneAfter, names)
			}
		})
	}
}

func TestAlias_miniFormatHeaderShowsAlias(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.ResponseFormat = "mini"
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	fake := fakeConnWithResp("list_pull_requests")
	proj := map[string]*config.ProjectionConfig{"list_pull_requests": {Alias: "list_prs"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh", "tool": "list_prs", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "[gh.list_prs]") {
		t.Errorf("mini format header should show alias, got: %s", text)
	}
	if strings.Contains(text, "list_pull_requests") {
		t.Errorf("mini format header should not expose real tool name, got: %s", text)
	}
}

func TestAlias_setProjectionPreservesAliasOnReload(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), "name: gh\n")

	fake := fakeConn("get_pr")
	proj := map[string]*config.ProjectionConfig{"get_pr": {Alias: "pr"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "gh", "tool": "pr",
		"projection": map[string]any{"exclude_always": []string{"body"}},
	}))
	if text := toolResultText(t, resp); strings.Contains(text, "error") {
		t.Fatalf("set_projection failed: %s", text)
	}

	serve(t, srv, callTool("config", map[string]any{"action": "reload"}))

	names := listNames(t, srv)
	if !names["gh.pr"] {
		t.Errorf("alias should survive set_projection+reload, got: %v", names)
	}
	if names["gh.get_pr"] {
		t.Errorf("real name should not reappear after set_projection+reload, got: %v", names)
	}
}

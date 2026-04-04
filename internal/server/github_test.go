//go:build live

package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func setupGitHubMCP(t *testing.T, ctx context.Context, token string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 500
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := server.New(cfg, logger)
	t.Cleanup(srv.Close)
	sc := config.ServerConfig{
		Name: "github", Transport: "http",
		URL:     "https://api.githubcopilot.com/mcp/",
		Headers: map[string]string{"Authorization": "Bearer " + token},
		Permissions: &config.PermissionsConfig{Default: "open", Protected: []string{
			"create_pull_request", "merge_pull_request", "add_issue_comment",
			"push_files", "create_repository", "fork_repository", "delete_file", "create_branch",
		}},
	}
	t.Log("connecting to GitHub MCP...")
	if err := srv.AddUpstream(ctx, sc); err != nil {
		t.Fatalf("connect to GitHub MCP: %v", err)
	}
	return srv
}

func ghTestDiscover(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
	var tools []map[string]any
	if err := json.Unmarshal([]byte(text), &tools); err != nil {
		t.Fatalf("discover not JSON: %v\n%s", err, text)
	}
	if len(tools) == 0 {
		t.Error("expected GitHub tools, got none")
	}
}

func ghTestGetMe(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "github", "tool": "get_me", "params": map[string]any{},
	})))
	env := parseEnvelope(t, text)
	if env["ok"] != true {
		t.Errorf("expected ok=true, got: %v", env["ok"])
	}
}

func ghTestBlockedOnExec(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "github", "tool": "merge_pull_request",
		"params": map[string]any{"owner": "x", "repo": "y", "pullNumber": 1},
	})))
	if !strings.Contains(text, "perm_call") {
		t.Errorf("expected perm_call error, got: %s", text)
	}
}

func ghTestSearchIssueTools(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{"query": "issue"})))
	var tools []map[string]any
	json.Unmarshal([]byte(text), &tools) //nolint:errcheck
	if len(tools) == 0 {
		t.Error("expected at least one issue tool")
	}
}

func ghTestListIssues(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "github", "tool": "list_issues",
		"params": map[string]any{"owner": "anthropics", "repo": "claude-code"},
	})))
	if env := parseEnvelope(t, text); env["ok"] != true {
		t.Errorf("expected ok=true, got: %v", env["ok"])
	}
}

func ghTestStatusShowsGitHub(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
	var status map[string]any
	json.Unmarshal([]byte(text), &status) //nolint:errcheck
	if servers, _ := status["servers"].(map[string]any); servers["github"] == nil {
		t.Error("expected github in servers")
	}
}

func TestWithGitHubMCP(t *testing.T) {
	token := os.Getenv("GITHUB_PERSONAL_ACCESS_TOKEN")
	if token == "" {
		t.Skip("GITHUB_PERSONAL_ACCESS_TOKEN not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := setupGitHubMCP(t, ctx, token)
	t.Run("discover lists github tools", func(t *testing.T) { ghTestDiscover(t, srv) })
	t.Run("search for issue tools", func(t *testing.T) { ghTestSearchIssueTools(t, srv) })
	t.Run("get_me returns envelope", func(t *testing.T) { ghTestGetMe(t, srv) })
	t.Run("list_issues returns envelope", func(t *testing.T) { ghTestListIssues(t, srv) })
	t.Run("create_issue blocked on execute", func(t *testing.T) { ghTestBlockedOnExec(t, srv) })
	t.Run("configure status shows github", func(t *testing.T) { ghTestStatusShowsGitHub(t, srv) })
}

func dataKeys(env map[string]any) []string {
	d, _ := env["data"].(map[string]any)
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	return keys
}

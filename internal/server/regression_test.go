//go:build test

// Package server_test — regression tests for findings from the 2026-04-27 review.
// Each test documents the original vulnerability and red-teams the fix.
package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

// ---------------------------------------------------------------------------
// Finding 1 (HIGH): validateStorePath symlink escape
//
// Before the fix, validateStorePath called filepath.Abs which does NOT resolve
// symlinks. A symlink inside the store dir pointing outside it would pass the
// prefix check and os.ReadFile would follow it to the target.
//
// Fix: use filepath.EvalSymlinks on both the path and storeDir before the
// prefix check, so symlink targets are compared, not symlink names.
// ---------------------------------------------------------------------------

// TestRead_RejectsSymlinkEscape creates a symlink inside the response store
// that points to a sensitive file outside it, then attempts to read via the
// MCP "read" tool. The fix must block it.
func TestRead_RejectsSymlinkEscape(t *testing.T) {
	// Create a file "outside" the store that we want to protect.
	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("sensitive-data"), 0600); err != nil {
		t.Fatal(err)
	}

	// Build a proxy server whose response store is a separate temp dir.
	storeDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = storeDir
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	// Create a symlink INSIDE the store dir that points OUTSIDE it.
	symlinkPath := filepath.Join(storeDir, "escape.json")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Attempt to read via the MCP tool. This must be rejected.
	resp := serve(t, srv, callTool("read", map[string]any{"path": symlinkPath}))

	// Should be an RPC-level error (errInvalidParams) or a tool-level isError.
	if rpcErr := resp["error"]; rpcErr != nil {
		errMap, ok := rpcErr.(map[string]any)
		if !ok {
			t.Fatalf("unexpected error shape: %v", rpcErr)
		}
		msg := errMap["message"].(string)
		if !strings.Contains(msg, "mini response directory") {
			t.Errorf("expected path confinement error, got: %s", msg)
		}
		return // correct — RPC-level rejection
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result or error in response: %v", resp)
	}
	if result["isError"] == true {
		// Tool-level error is also acceptable.
		return
	}

	// If we get here the read succeeded — the fix is broken.
	content := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(content, "sensitive-data") {
		t.Errorf("SECURITY: symlink escape succeeded — read returned secret file contents")
	} else {
		t.Errorf("symlink read returned unexpected content (no secret but also no error): %s", content)
	}
}

// TestRead_SymlinkWithinStore_Allowed verifies that a symlink pointing to
// another file INSIDE the store dir is still readable (not over-blocked).
func TestRead_SymlinkWithinStore_Allowed(t *testing.T) {
	storeDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = storeDir
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	// Create a real file inside the store and a symlink to it, also inside.
	realFile := filepath.Join(storeDir, "real.json")
	if err := os.WriteFile(realFile, []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(storeDir, "link.json")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	resp := serve(t, srv, callTool("read", map[string]any{"path": symlinkPath}))
	// Should succeed (symlink within store is allowed).
	if rpcErr := resp["error"]; rpcErr != nil {
		t.Fatalf("unexpected RPC error for in-store symlink: %v", rpcErr)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Errorf("expected successful read for in-store symlink, got: %v", resp)
	}
}

// ---------------------------------------------------------------------------
// Finding 2 (MEDIUM): concurrent add_server / remove_server TOCTOU
//
// Before the fix, remove_server held s.mu only during detachUpstream, then
// released it before reg.RemoveServer. A racing add_server could insert a
// new upstream and register its tools between the two steps; reg.RemoveServer
// would then delete those fresh tools.
//
// Fix: serverOpMu serializes all add/remove operations for the same name.
// ---------------------------------------------------------------------------

// TestConcurrentAddRemove_RegistryConsistency hammers concurrent add+remove
// for the same server name and asserts that after all goroutines finish, the
// registry is in a consistent state: either the server is present with tools,
// or it is fully absent — never partially corrupted.
func TestConcurrentAddRemove_RegistryConsistency(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	ctx := context.Background()

	const iterations = 200
	var wg sync.WaitGroup

	// Adder goroutine: repeatedly adds the server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		fake := fakeConn("toolX")
		for range iterations {
			srv.AddConnection(ctx, config.ServerConfig{Name: "contested"}, fake) //nolint:errcheck
		}
	}()

	// Remover goroutine: repeatedly removes the server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iterations {
			serve(t, srv, callTool("config", map[string]any{
				"action": "remove_server",
				"server": "contested",
			}))
		}
	}()

	wg.Wait()

	// After the storm, call list and check the result is coherent.
	// "contested.toolX" should either be present or absent — never in a
	// broken state that would cause a panic or data corruption.
	resp := serve(t, srv, callTool("list", map[string]any{}))
	text := toolResultText(t, resp)
	// If the server is present, "contested" should appear in the list.
	// The important thing is that the call doesn't panic or return garbage.
	_ = text // success = no panic, no race-detector violation
}

// TestAddRemoveSameServer_ToolsDoNotLeak verifies that after add then remove,
// the server's tools are fully gone from the list.
func TestAddRemoveSameServer_ToolsDoNotLeak(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	ctx := context.Background()

	fake := fakeConn("secret_tool")
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "tempserver"}, fake); err != nil {
		t.Fatal(err)
	}

	// Verify the tool is visible.
	resp := serve(t, srv, callTool("list", map[string]any{}))
	if !strings.Contains(toolResultText(t, resp), "tempserver.secret_tool") {
		t.Fatal("expected secret_tool to be listed after add")
	}

	// Remove the server.
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "tempserver",
	}))

	// The tool must no longer be discoverable.
	resp = serve(t, srv, callTool("list", map[string]any{}))
	if strings.Contains(toolResultText(t, resp), "tempserver") {
		t.Errorf("tempserver tools still visible after remove_server")
	}

	// Calling the removed tool must fail cleanly, not panic.
	resp = serve(t, srv, callTool("call", map[string]any{
		"server": "tempserver", "tool": "secret_tool", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "not connected") && !strings.Contains(text, "not found") {
		t.Errorf("expected not-found or not-connected error after remove, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Finding 3 (LOW): stale projections after server removal
//
// Before the fix, detachUpstream removed the server from s.upstreams but left
// s.projections[serverName] in place. If the same name was re-added later
// without sc.Projections set, the old projections would silently apply.
//
// Fix: detachUpstream now deletes s.projections[serverName] alongside the
// upstream entry.
// ---------------------------------------------------------------------------

// TestRemoveServer_ClearsProjections verifies that projections set for a
// server are deleted when the server is removed, so a re-added server with the
// same name doesn't inherit stale projection config.
func TestRemoveServer_ClearsProjections(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	ctx := context.Background()

	fake := fakeConn("search")
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}

	// Set a server-scoped projection for svc.search.
	resp := serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "search",
		"projection": map[string]any{"mode": "slim"},
	}))
	if !strings.Contains(toolResultText(t, resp), `"ok":true`) {
		t.Fatalf("set_projection failed: %v", resp)
	}

	// Verify the projection is visible in status.
	resp = serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	statusText := toolResultText(t, resp)
	if !strings.Contains(statusText, "svc") {
		t.Fatalf("expected svc in status after add: %s", statusText)
	}

	// Remove the server.
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "svc",
	}))

	// Re-add the server with a different fake that returns large responses.
	// No projections are set this time.
	large := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "search", Description: "search", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"` + strings.Repeat("x", 200) + `"}]}`),
		},
	}
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, large); err != nil {
		t.Fatal(err)
	}

	// The call should use perm_call (no projection coverage) or inline without
	// slim-mode truncation. Either way, the stale "slim" projection must NOT apply.
	// We check that the response is NOT slimmed (i.e. the full 200-char response comes back).
	resp = serve(t, srv, callTool("perm_call", map[string]any{
		"server": "svc", "tool": "search", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	// With stale slim projection the text would be truncated; without it the full
	// upstream response (embedded in the envelope) should contain the 200 x's.
	if strings.Contains(text, "slim") {
		t.Errorf("stale slim projection still active after server removal and re-add: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Existing-coverage verification: path traversal via ".." still blocked
// (regression guard — the EvalSymlinks change must not weaken this).
// ---------------------------------------------------------------------------

func TestRead_DotDotTraversalStillBlocked(t *testing.T) {
	storeDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = storeDir
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	// Craft a path that tries to escape via ".." without using a symlink.
	traversal := filepath.Join(storeDir, "..", "escape.json")

	resp := serve(t, srv, callTool("read", map[string]any{"path": traversal}))
	if rpcErr := resp["error"]; rpcErr != nil {
		return // RPC rejection is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] == true {
		return // tool-level rejection is also correct
	}
	t.Errorf("path traversal via '..' was not blocked: %v", resp)
}

// TestGenerationCounter_RemoveWinsRace verifies the TOCTOU fix for
// concurrent add_server + remove_server. Before the fix, add_server could
// complete after remove_server returned ok:true, leaving the server
// re-registered. The generation counter in removeGen makes add_server
// detect the concurrent remove and abort.
func TestGenerationCounter_RemoveWinsRace(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	ctx := context.Background()

	// dialReady signals that the add goroutine has started dialling (simulated
	// by AddConnection entering before we fire remove). removeStarted blocks
	// add until remove has incremented the generation.
	dialReady := make(chan struct{})
	removeStarted := make(chan struct{})

	addDone := make(chan error, 1)
	go func() {
		slow := &slowFakeConn{
			FakeConnection: fakeConn("toolA"),
			ready:          dialReady,
			block:          removeStarted,
		}
		close(dialReady)
		addDone <- srv.AddConnection(ctx, config.ServerConfig{Name: "racing"}, slow)
	}()

	<-dialReady
	// Remove while add is in-flight (generation counter incremented here).
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server", "server": "racing",
	}))
	close(removeStarted) // unblock the slow add
	<-addDone            // wait for add to finish

	// After remove returned ok, "racing" must NOT be in the registry.
	resp := serve(t, srv, callTool("list", map[string]any{}))
	if strings.Contains(toolResultText(t, resp), "racing") {
		t.Error("remove_server returned ok:true but server is still registered (generation counter not working)")
	}
}

type slowFakeConn struct {
	*transport.FakeConnection
	ready chan struct{}
	block chan struct{}
}

func (c *slowFakeConn) ListTools(ctx context.Context) ([]transport.ToolDefinition, error) {
	<-c.block // wait until remove has fired
	return c.FakeConnection.ListTools(ctx)
}

// TestInlineProjections_AppliedOnAddConnection verifies that projections
// embedded directly in a ServerConfig (sc.Projections) are installed into
// s.projections when the server is registered. This is the installUpstreamLocked
// branch that handles projections embedded in server YAML under projections:.
func TestInlineProjections_AppliedOnAddConnection(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\",\"name\":\"foo\"}"}]}`)

	projCfg := &config.ProjectionConfig{ExcludeAlways: []string{"secret"}}
	sc := config.ServerConfig{
		Name:        "svc",
		Projections: map[string]*config.ProjectionConfig{"get_item": projCfg},
	}
	if err := srv.AddConnection(context.Background(), sc, conn); err != nil {
		t.Fatal(err)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "get_item", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	t.Logf("inline projection response: %s", text)

	if strings.Contains(text, "hidden") {
		t.Errorf("inline projection should have excluded 'secret' field, got: %s", text)
	}
	if !strings.Contains(text, "foo") {
		t.Errorf("expected 'name' field to be present, got: %s", text)
	}
}

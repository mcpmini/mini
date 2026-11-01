//go:build test

package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestProxy_MiniRead_ReadsFile(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	text1 := toolResultText(t, resp1)
	t.Logf("initial response: %s", text1)

	var env map[string]any
	if err := json.Unmarshal([]byte(text1), &env); err != nil {
		t.Fatalf("expected JSON envelope from projected call, got: %s", text1)
	}
	mini, _ := env["__mini"].(map[string]any)
	if mini == nil {
		t.Fatal("expected __mini key — exclusion of 'secret' should produce projection envelope")
	}
	key, _ := mini["file"].(string)
	if key == "" {
		t.Fatalf("expected __mini.file to be set, got: %s", text1)
	}

	resp2 := serve(t, srv, callTool("read", map[string]any{"file": key}))
	text2 := toolResultText(t, resp2)
	t.Logf("read response: %s", text2)

	if text2 == "" {
		t.Error("read returned empty content")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text2), &parsed); err != nil {
		t.Errorf("read content should be JSON: %s", text2)
	}
}

func TestProxy_MiniRead_WithFilter(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_data")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":99,\"name\":\"widget\",\"secret\":\"x\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp1 := serve(t, srv, callTool("svc__get_data", map[string]any{}))
	text1 := toolResultText(t, resp1)
	var env map[string]any
	_ = json.Unmarshal([]byte(text1), &env)
	key, _ := env["__mini"].(map[string]any)["file"].(string)
	if key == "" {
		t.Fatalf("expected file key in __mini, got: %s", text1)
	}

	resp2 := serve(
		t,
		srv,
		callTool("read", map[string]any{"file": key, "filter": ".content[0].text | fromjson | .name"}),
	)
	text2 := toolResultText(t, resp2)
	if text2 != `"widget"` {
		t.Errorf("filter .content[0].text | fromjson | .name: expected %q, got %q", `"widget"`, text2)
	}
}

func TestProxy_MiniRead_InvalidFilterReturnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_data2")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data2",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp1 := serve(t, srv, callTool("svc__get_data2", map[string]any{}))
	text1 := toolResultText(t, resp1)
	var env map[string]any
	_ = json.Unmarshal([]byte(text1), &env)
	key, _ := env["__mini"].(map[string]any)["file"].(string)
	if key == "" {
		t.Fatalf("expected file key in __mini, got: %s", text1)
	}

	resp2 := serve(t, srv, callTool("read", map[string]any{"file": key, "filter": "!!!invalid_jq!!!"}))
	if resp2["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp2["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for invalid jq filter, got: %v", resp2)
	}
}

func TestProxy_MiniRead_FilenameOnly(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_thing")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":5,\"secret\":\"x\"}"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_thing",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp1 := serve(t, srv, callTool("svc__get_thing", map[string]any{}))
	text1 := toolResultText(t, resp1)
	var env map[string]any
	_ = json.Unmarshal([]byte(text1), &env)
	key, _ := env["__mini"].(map[string]any)["file"].(string)
	if key == "" {
		t.Fatalf("expected file key in __mini, got: %s", text1)
	}
	if strings.ContainsAny(key, "/\\") || strings.HasSuffix(key, ".json") {
		t.Errorf("__mini.file should be a bare key with no path or extension, got: %s", key)
	}

	resp2 := serve(t, srv, callTool("read", map[string]any{"file": key}))
	text2 := toolResultText(t, resp2)
	if text2 == "" {
		t.Error("read returned empty content using bare key")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text2), &parsed); err != nil {
		t.Errorf("read content via bare key should be JSON: %s", text2)
	}
}

func TestProxy_MiniRead_RejectsPathTraversal(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	for _, path := range []string{"../../etc/passwd", "../secrets.json", "../../etc/shadow.json"} {
		resp := serve(t, srv, callTool("read", map[string]any{"file": path}))
		if resp["error"] != nil {
			continue
		}
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("path %q: expected error for traversal attempt, got: %v", path, resp)
		}
	}
}

func TestProxy_MiniRead_FileNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	resp := serve(t, srv, callTool("read", map[string]any{"file": "9999999999999"}))
	if resp["error"] != nil {
		return // RPC-level error is also acceptable
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for non-existent file, got: %v", resp)
	}
	text := toolResultText(t, resp)
	if strings.Contains(text, cfg.ResponseDir) {
		t.Errorf("error message must not expose store path, got: %s", text)
	}
}

func TestProxy_MiniRead_RequiresPath(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	resp := serve(t, srv, callTool("read", map[string]any{}))
	if resp["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Error("expected error when path is empty")
	}
}

func TestProxy_UnknownTool_ReturnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	resp := serve(t, srv, callTool("nonexistent__tool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected error for unknown proxy tool, got: %v", resp)
		}
	}
}

func TestProxy_NoDoubleUnderscore_ReturnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	resp := serve(t, srv, callTool("notaproxytool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected error for malformed proxy tool name, got: %v", resp)
		}
	}
}

func TestProxy_MiniRead_RecoversExcludedFieldFromToonNotice(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	setProxyExclude(
		setProxyExcludeParams{
			T:          t,
			Server:     srv,
			Tool:       "get_user",
			Projection: map[string]any{"exclude": []string{"secret"}},
		},
	)

	texts := contentTexts(proxyResultOf(t, serve(t, srv, callTool("svc__get_user", map[string]any{}))))
	match := regexp.MustCompile(`\n  file: "(\d+)"\n`).FindStringSubmatch(strings.Join(texts, "\n"))
	if match == nil {
		t.Fatalf("TOON notice has no quoted file key: %q", texts)
	}
	const path = ".content[0].text | fromjson | .secret"
	read := callTool("read", map[string]any{"file": match[1], "filter": path})
	if recovered := toolResultText(t, serve(t, srv, read)); recovered != `"x"` {
		t.Errorf("read(%q) = %q, want the excluded value", path, recovered)
	}
}

// TestRead_RejectsSymlinkEscape creates a symlink inside the response store
// that points to a sensitive file outside it, then attempts to read via the
// MCP "read" tool. The fix must block it.
func TestRead_RejectsSymlinkEscape(t *testing.T) {
	// Create a file "outside" the store that we want to protect.
	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	testutil.WriteFile(t, secretFile, "sensitive-data")

	// Build a proxy server whose response store is a separate temp dir.
	storeDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = storeDir
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	// Create a symlink INSIDE the store dir that points OUTSIDE it.
	symlinkPath := filepath.Join(storeDir, "escape.json")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Attempt to read via the MCP tool. This must be rejected.
	resp := serve(t, srv, callTool("read", map[string]any{"file": filepath.Base(symlinkPath)}))

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
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	// Create a real file inside the store and a symlink to it, also inside.
	realFile := filepath.Join(storeDir, "real.json")
	testutil.WriteFile(t, realFile, `{"ok":true}`)
	symlinkPath := filepath.Join(storeDir, "link.json")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	resp := serve(t, srv, callTool("read", map[string]any{"file": filepath.Base(symlinkPath)}))
	// Should succeed (symlink within store is allowed).
	if rpcErr := resp["error"]; rpcErr != nil {
		t.Fatalf("unexpected RPC error for in-store symlink: %v", rpcErr)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Errorf("expected successful read for in-store symlink, got: %v", resp)
	}
}

func TestRead_DotDotTraversalStillBlocked(t *testing.T) {
	storeDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = storeDir
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	// Craft a relative path that tries to escape via ".." without using a symlink.
	resp := serve(t, srv, callTool("read", map[string]any{"file": "../escape.json"}))
	if rpcErr := resp["error"]; rpcErr != nil {
		return // RPC rejection is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] == true {
		return // tool-level rejection is also correct
	}
	t.Errorf("path traversal via '..' was not blocked: %v", resp)
}

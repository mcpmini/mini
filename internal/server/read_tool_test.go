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

// storedResponse calls svc__get_item, whose payload has "secret" excluded, and returns the
// file key of the raw response mini stored.
func storedResponse(t *testing.T, payload string) (*proxyHarness, string) {
	t.Helper()
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(payload), "get_item")
	h.setProjection("svc", "get_item", excludeSecret)
	text := h.callText("svc__get_item", nil)
	file := parseProxyEnvelope(t, text).File
	if file == "" {
		t.Fatalf("expected __mini.file after excluding secret, got: %s", text)
	}
	return h, file
}

func rejected(resp map[string]any) bool {
	result, _ := resp["result"].(map[string]any)
	return resp["error"] != nil || result["isError"] == true
}

func TestProxy_MiniRead_ReadsFile(t *testing.T) {
	h, file := storedResponse(t, `{"id":1,"secret":"hidden"}`)

	text := h.callText("read", map[string]any{"file": file})

	if !json.Valid([]byte(text)) || !strings.Contains(text, "hidden") {
		t.Errorf("read should return the stored upstream response as JSON, excluded value included: %s", text)
	}
}

func TestProxy_MiniRead_WithFilter(t *testing.T) {
	h, file := storedResponse(t, `{"id":99,"name":"widget","secret":"x"}`)

	text := h.callText("read", map[string]any{"file": file, "filter": ".content[0].text | fromjson | .name"})

	if text != `"widget"` {
		t.Errorf("filter .content[0].text | fromjson | .name: expected %q, got %q", `"widget"`, text)
	}
}

func TestProxy_MiniRead_InvalidFilterReturnsError(t *testing.T) {
	h, file := storedResponse(t, `{"id":1,"secret":"x"}`)

	resp := serve(t, h.srv, callTool("read", map[string]any{"file": file, "filter": "!!!invalid_jq!!!"}))

	if !rejected(resp) {
		t.Errorf("expected error for invalid jq filter, got: %v", resp)
	}
}

func TestProxy_MiniRead_FilenameOnly(t *testing.T) {
	h, file := storedResponse(t, `{"id":5,"secret":"x"}`)

	if strings.ContainsAny(file, "/\\") || strings.HasSuffix(file, ".json") {
		t.Errorf("__mini.file should be a bare key with no path or extension, got: %s", file)
	}
	if text := h.callText("read", map[string]any{"file": file}); !json.Valid([]byte(text)) || text == "" {
		t.Errorf("read content via bare key should be JSON: %s", text)
	}
}

func TestProxy_MiniRead_RejectsPathTraversal(t *testing.T) {
	srv := newTestServer(t, server.Params{})

	for _, path := range []string{"../../etc/passwd", "../secrets.json", "../../etc/shadow.json"} {
		if resp := serve(t, srv, callTool("read", map[string]any{"file": path})); !rejected(resp) {
			t.Errorf("path %q: expected error for traversal attempt, got: %v", path, resp)
		}
	}
}

func TestProxy_MiniRead_FileNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := newTestServer(t, server.Params{Config: cfg})

	resp := serve(t, srv, callTool("read", map[string]any{"file": "9999999999999"}))

	if !rejected(resp) {
		t.Fatalf("expected error for non-existent file, got: %v", resp)
	}
	if text, _ := json.Marshal(resp); strings.Contains(string(text), cfg.ResponseDir) {
		t.Errorf("error message must not expose store path, got: %s", text)
	}
}

func TestProxy_MiniRead_RequiresPath(t *testing.T) {
	srv := newTestServer(t, server.Params{})

	if resp := serve(t, srv, callTool("read", map[string]any{})); !rejected(resp) {
		t.Errorf("expected error when path is empty, got: %v", resp)
	}
}

func TestProxy_UnknownTool_ReturnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})

	if resp := serve(t, srv, callTool("nonexistent__tool", map[string]any{})); !rejected(resp) {
		t.Errorf("expected error for unknown proxy tool, got: %v", resp)
	}
}

func TestProxy_NoDoubleUnderscore_ReturnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})

	if resp := serve(t, srv, callTool("notaproxytool", map[string]any{})); !rejected(resp) {
		t.Errorf("expected error for malformed proxy tool name, got: %v", resp)
	}
}

func TestProxy_MiniRead_RecoversExcludedFieldFromToonNotice(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"x"}`), "get_user")
	h.setProjection("svc", "get_user", excludeSecret)

	texts := contentTexts(h.call("svc__get_user", nil))

	match := regexp.MustCompile(`\n  file: "(\d+)"\n`).FindStringSubmatch(strings.Join(texts, "\n"))
	if match == nil {
		t.Fatalf("TOON notice has no quoted file key: %q", texts)
	}
	const path = ".content[0].text | fromjson | .secret"
	if recovered := h.callText("read", map[string]any{"file": match[1], "filter": path}); recovered != `"x"` {
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

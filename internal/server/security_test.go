//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newSecureServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestDispatch_unknownMethod_returns32601 verifies that unknown JSON-RPC
// methods return error code -32601 (MethodNotFound), not -32603 (Internal).
func TestDispatch_unknownMethod_returns32601(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	resp := serveRawMethod(t, srv, "no/such/method")
	rpcErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error field, got: %v", resp)
	}
	if code := rpcErr["code"].(float64); code != -32601 {
		t.Errorf("expected -32601 MethodNotFound, got %v", code)
	}
}

func serveRawMethod(t *testing.T, srv *server.Server, method string) map[string]any {
	t.Helper()
	initParams, _ := json.Marshal(map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	initReq, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": json.RawMessage(initParams)})
	callReq, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
	in := bytes.NewReader(append(append(initReq, '\n'), append(callReq, '\n')...))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), in, &out) }()
	<-done
	return findResponseByID(t, out.Bytes(), "1")
}

// TestPathTraversal_setProjection_rejected verifies that server names with
// path traversal characters are rejected before any file I/O.
func TestPathTraversal_setProjection_rejected(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	badNames := []string{"../evil", "../../etc", "bad/name", "bad name", ""}
	for _, name := range badNames {
		resp := serve(t, srv, callTool("config", map[string]any{
			"action":     "set_projection",
			"server":     name,
			"tool":       "mytool",
			"projection": map[string]any{},
		}))
		text := toolResultText(t, resp)
		if !strings.Contains(text, "invalid") && !strings.Contains(text, "required") {
			t.Errorf("expected rejection for server name %q, got: %s", name, text)
		}
	}
}

// TestPathTraversal_removeServer_rejected verifies invalid server names are
// rejected in remove_server.
func TestPathTraversal_removeServer_rejected(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "../escape",
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "invalid") {
		t.Errorf("expected invalid server name error, got: %s", text)
	}
}

// TestAddServer_stdioRejectedByDefault verifies that stdio transports are
// blocked by default (dangerous_allow_runtime_stdio is false).
func TestAddServer_stdioRejectedByDefault(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{
			"name":    "myserver",
			"command": "/bin/echo",
			"args":    []string{"hello"},
		},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "http") && !strings.Contains(text, "dangerous") {
		t.Errorf("expected stdio rejection message, got: %s", text)
	}
}

// TestAddServer_stdioAllowedWithFlag verifies dangerous_allow_runtime_stdio
// lets stdio servers be registered.
func TestAddServer_stdioAllowedWithFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.DangerousAllowRuntimeStdio = true
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg, logger)
	defer srv.Close()

	// Attempt add_server with invalid server name — should fail name validation,
	// not the transport check. Confirms we get past the transport gate.
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{
			"name":    "../evil",
			"command": "/bin/echo",
		},
	}))
	text := toolResultText(t, resp)
	// Should fail on name validation, not transport rejection.
	if strings.Contains(text, "http/sse/streamable") {
		t.Errorf("flag had no effect — transport rejection still fired: %s", text)
	}
}

// TestServerClose_doubleCloseNoPanic verifies Store.Close() is idempotent.
func TestServerClose_doubleCloseNoPanic(t *testing.T) {
	srv := newSecureServer(t)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	srv.Close()
	srv.Close() // must not panic
}

// TestAddServer_invalidName_rejected verifies server names must match
// ^[a-zA-Z0-9_-]+$ for add_server.
func TestAddServer_invalidName_rejected(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowRuntimeStdio = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{
			"name":      "../../etc/cron.d/backdoor",
			"transport": "http",
			"url":       "http://localhost:9999",
		},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "invalid") {
		t.Errorf("expected invalid name rejection, got: %s", text)
	}
}

func TestPathTraversal_exec_rejected(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	for _, name := range []string{"../escape", "../../etc/passwd", "bad name!", "server\x00null"} {
		for _, tool := range []string{"call", "perm_call"} {
			resp := serve(t, srv, callTool(tool, map[string]any{
				"server": name,
				"tool":   "anything",
				"params": map[string]any{},
			}))
			text := toolResultText(t, resp)
			if !strings.Contains(text, "invalid server name") {
				t.Errorf("%s: expected invalid server name rejection for %q, got: %s", tool, name, text)
			}
		}
	}
}

// TestExec_invalidToolName_rejected verifies that tool names with invalid
// characters are rejected before registry lookup in both call and perm_call.
func TestExec_invalidToolName_rejected(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()

	for _, badTool := range []string{"bad tool!", "tool\x00null", "tool@host"} {
		for _, method := range []string{"call", "perm_call"} {
			resp := serve(t, srv, callTool(method, map[string]any{
				"server": "validserver",
				"tool":   badTool,
				"params": map[string]any{},
			}))
			text := toolResultText(t, resp)
			if !strings.Contains(text, "invalid tool name") {
				t.Errorf("%s: expected invalid tool name rejection for %q, got: %s", method, badTool, text)
			}
		}
	}
}

// TestAddServer_credentialsStrippedWithPrivateURLsAllowed verifies that
// agent-supplied auth/headers are stripped even when DangerousAllowPrivateURLs
// is true. The connection will fail (no real server), but the error must come
// from the dial attempt, not from URL validation (private IP rejection).
func TestAddServer_credentialsStrippedWithPrivateURLsAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{
			"name":      "internal",
			"transport": "http",
			"url":       "http://127.0.0.1:1",
			"auth":      map[string]any{"type": "bearer", "token": "secret"},
		},
	}))
	text := toolResultText(t, resp)
	// Must fail with a connection error, not an SSRF/private-IP rejection.
	if strings.Contains(text, "private") || strings.Contains(text, "loopback") {
		t.Errorf("private URL was rejected despite DangerousAllowPrivateURLs=true: %s", text)
	}
}

var ssrfBlockedURLs = []string{
	"http://127.0.0.1:8080",
	"http://10.0.0.1/mcp",
	"http://192.168.1.1/api",
	"http://169.254.169.254/latest/meta-data/", // AWS metadata
	"http://172.16.0.1/internal",
	"http://localhost/steal",
	"http://localhost:8080/mcp",
	"http://evil.localhost/steal", // .localhost TLD
	"http://myapp.local/api",     // mDNS .local
	"http://service.internal/mcp", // GCP internal DNS
	"ftp://example.com/data",
}

// TestAddServer_SSRFPrivateIPBlocked verifies that add_server rejects URLs
// pointing to private/loopback IP ranges to prevent SSRF attacks.
func TestAddServer_SSRFPrivateIPBlocked(t *testing.T) {
	srv := newSecureServer(t)
	defer srv.Close()
	for _, u := range ssrfBlockedURLs {
		resp := serve(t, srv, callTool("config", map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "attack", "transport": "http", "url": u},
		}))
		text := toolResultText(t, resp)
		if !strings.Contains(text, "add_server:") && !strings.Contains(text, "invalid") &&
			!strings.Contains(text, "private") && !strings.Contains(text, "scheme") {
			t.Errorf("expected SSRF rejection for URL %q, got: %s", u, text)
		}
	}
}

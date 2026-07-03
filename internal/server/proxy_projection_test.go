//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newProxyServerWithSecretTool(t *testing.T, stringLimit int) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	if stringLimit > 0 {
		cfg.DefaultStringLimit = stringLimit
	}
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\",\"body\":\"` + strings.Repeat("x", 80) + `\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)
	return srv
}

func TestProxy_RawProjection_BypassesConfiguredExclusion(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
	text := toolResultText(t, resp)
	t.Logf("raw response: %s", text)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("raw projection must produce no __mini metadata, got: %s", text)
	}
	env := parseProxyEnvelope(t, text)
	if env.Data["secret"] != "hidden" {
		t.Errorf("raw projection must bypass configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_RawProjection_BypassesGlobalStringLimit(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 10)
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
	text := toolResultText(t, resp)

	if !strings.Contains(text, strings.Repeat("x", 80)) {
		t.Errorf("raw projection must bypass global DefaultStringLimit, got: %s", text)
	}
}

func TestProxy_DefaultProjection_UsesConfiguredExclusion(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "default"}}))
	text := toolResultText(t, resp)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatalf("expected __mini for default projection with configured exclusion, got: %s", text)
	}
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("default projection must apply configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_OmittedProjectionControl_BehavesAsDefault(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)

	env := parseProxyEnvelope(t, text)
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("omitted __mini.projection must behave as default, got data: %v", env.Data)
	}
}

func TestProxy_InvalidProjectionControl_RejectedAsToolError(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "bogus"}}))
	requireRPCError(t, resp, -32602, "projection")
}

func TestProxy_ConcurrentRawAndDefaultCalls_DoNotCrossContaminate(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
			env := parseProxyEnvelope(t, toolResultText(t, resp))
			if env.Data["secret"] != "hidden" {
				t.Errorf("raw call lost secret field under concurrency: %v", env.Data)
			}
		}()
		go func() {
			defer wg.Done()
			resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "default"}}))
			env := parseProxyEnvelope(t, toolResultText(t, resp))
			if _, hasSecret := env.Data["secret"]; hasSecret {
				t.Errorf("default call leaked secret field under concurrency: %v", env.Data)
			}
		}()
	}
	wg.Wait()
}

func TestProxy_DefaultProjection_PreservesLargeIntegers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":9007199254740993,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "9007199254740993") {
		t.Errorf("default projection corrupted large integer: %s", text)
	}
}

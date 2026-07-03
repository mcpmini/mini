//go:build test

package server_test

import (
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newCodeModeServer(t *testing.T, enabled bool) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.ExperimentalCodeMode = enabled
	srv := server.New(cfg, logger)
	t.Cleanup(srv.Close)
	return srv
}

func compactToolNames(t *testing.T, srv *server.Server) []string {
	t.Helper()
	resp := serve(t, srv, rpc("tools/list", nil))
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.(map[string]any)["name"].(string)
	}
	return names
}

func TestExecuteCode_Disabled(t *testing.T) {
	srv := newCodeModeServer(t, false)

	t.Run("omitted from tools/list", func(t *testing.T) {
		names := compactToolNames(t, srv)
		for _, name := range names {
			if name == "execute_code" {
				t.Fatalf("expected execute_code to be absent, got tools: %v", names)
			}
		}
	})

	t.Run("call rejected as unknown tool", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{"code": "async (input) => input"}))
		errObj, _ := resp["error"].(map[string]any)
		if errObj == nil {
			t.Fatalf("expected JSON-RPC error, got: %v", resp)
		}
		if msg, _ := errObj["message"].(string); !strings.Contains(msg, "unknown tool") {
			t.Errorf("expected message to mention unknown tool, got: %v", msg)
		}
	})
}

func TestExecuteCode_EnabledToolsList(t *testing.T) {
	srv := newCodeModeServer(t, true)

	t.Run("compact mode", func(t *testing.T) {
		names := compactToolNames(t, srv)
		if !containsString(names, "execute_code") {
			t.Errorf("expected execute_code in compact tools/list, got: %v", names)
		}
	})

	t.Run("proxy mode", func(t *testing.T) {
		tools := toolsList(t, srv)
		if !containsName(tools, "execute_code") {
			t.Errorf("expected execute_code in proxy tools/list, got: %v", toolNames(tools))
		}
	})
}

func TestExecuteCode_MissingCode(t *testing.T) {
	srv := newCodeModeServer(t, true)
	resp := serve(t, srv, callTool("execute_code", map[string]any{}))
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected JSON-RPC error, got: %v", resp)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "requires code") {
		t.Errorf("expected message to mention required code, got: %v", msg)
	}
}

func TestExecuteCode_WithDeno(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
	srv := newCodeModeServer(t, true)

	t.Run("trivial call returns computed result", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code":  "async (input) => input.n + 1",
			"input": map[string]any{"n": 41},
		}))
		text := toolResultText(t, resp)
		if text != "42" {
			t.Errorf("expected result text %q, got %q", "42", text)
		}
	})

	t.Run("syntax error surfaces as isError", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": "async (input) =>",
		}))
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["isError"] != true {
			t.Fatalf("expected isError=true, got: %v", resp)
		}
		text := toolResultText(t, resp)
		if !strings.Contains(text, "syntax") {
			t.Errorf("expected error text to mention syntax, got: %v", text)
		}
	})
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

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
)

func TestCompactToonDepthErrorIsToolError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		tool   string
		perm   *config.PermissionsConfig
	}{
		{name: "call open", method: "call", tool: "deep_open"},
		{name: "perm_call protected", method: "perm_call", tool: "deep_protected", perm: &config.PermissionsConfig{Protected: []string{"deep_protected"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newDeepToonServer(t, tc.perm)
			resp := serve(t, srv, callTool(tc.method, map[string]any{
				"server": "svc", "tool": tc.tool, "params": map[string]any{},
			}))
			requireToolError(t, resp, "encode TOON response")
			text := toolResultText(t, resp)
			if strings.Contains(text, "_toon_fallback") || strings.Contains(text, "depth-sentinel") {
				t.Fatalf("unexpected fallback or serialized response data: %q", text)
			}
		})
	}
}

func newDeepToonServer(t *testing.T, perm *config.PermissionsConfig) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir, cfg.ResponseFormat = t.TempDir(), "toon"
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	fake := fakeConn("deep_open", "deep_protected")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + deepPayload(t) + `}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", Permissions: perm}, fake)
	return srv
}

func deepPayload(t *testing.T) string {
	t.Helper()
	value := map[string]any{"depth-sentinel": "must-not-be-serialized"}
	for i := 0; i < 70; i++ {
		value = map[string]any{"nested": value, "sibling": i}
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal deep payload: %v", err)
	}
	return string(mustJSONMarshal(t, string(data)))
}

func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response text: %v", err)
	}
	return data
}

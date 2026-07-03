//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func TestMiniFormat_AllArrayShapes(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantHdr  bool // expect mini format header row
		wantKeys []string
	}{
		{"uniform", `[{"number":1,"state":"open","title":"Bug"},{"number":2,"state":"closed","title":"Feat"}]`,
			true, []string{"number", "state", "title"}},
		{"non-uniform (diff keys)", `[{"number":1,"title":"Bug","labels":["bug"]},{"number":2,"title":"Feat"}]`,
			false, nil},
		{"single item (no header)", `[{"number":1,"title":"Solo","state":"open"}]`,
			false, nil},
		{"string array", `["alpha","beta","gamma"]`, false, nil},
		{"wrapped map+array", `{"total":3,"items":[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]}`,
			true, []string{"id", "name"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer srv.Close()

			conn := fakeConn("list")
			conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + string(mustJSON(tc.raw)) + `}]}`)
			addTestConnection(t, srv, config.ServerConfig{Name: "svc"}, conn)
			serve(t, srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "list",
				"projection": map[string]any{"format": "mini"},
			}))

			resp := serve(t, srv, callTool("call", map[string]any{
				"server": "svc", "tool": "list", "params": map[string]any{},
			}))
			text := toolResultText(t, resp)
			t.Logf("output: %q", text)

			if !strings.Contains(text, "svc.list") {
				t.Errorf("missing [svc.list] header: %s", text)
			}
			if tc.wantHdr {
				for _, k := range tc.wantKeys {
					if !strings.Contains(text, k) {
						t.Errorf("expected header key %q in mini format output: %s", k, text)
					}
				}
			}
		})
	}
}

func TestMiniFormat_NumericValuesPreserved(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantValues []string
	}{
		{"uniform array", `[{"number":7,"title":"Bug"},{"number":9,"title":"Feat"}]`, []string{"7", "9", "Bug", "Feat"}},
		{"non-uniform array", `[{"number":7,"title":"Bug","labels":["x"]},{"number":9,"title":"Feat"}]`, []string{"7", "9", "Bug", "Feat"}},
		{"single item", `[{"id":42,"name":"solo"}]`, []string{"42", "solo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer srv.Close()

			conn := fakeConn("list")
			conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + string(mustJSON(tc.raw)) + `}]}`)
			addTestConnection(t, srv, config.ServerConfig{Name: "svc"}, conn)
			serve(t, srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "list",
				"projection": map[string]any{"format": "mini"},
			}))

			resp := serve(t, srv, callTool("call", map[string]any{
				"server": "svc", "tool": "list", "params": map[string]any{},
			}))
			text := toolResultText(t, resp)
			for _, v := range tc.wantValues {
				if !strings.Contains(text, v) {
					t.Errorf("expected value %q in mini format output: %s", v, text)
				}
			}
		})
	}
}

func TestMiniFormat_ZeroValueSuppressedAcrossRenderPaths(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		notWant string
	}{
		{"uniform array table row", `[{"count":0,"title":"Bug"},{"count":9,"title":"Feat"}]`, "- Bug", "0 Bug"},
		{"single item line", `[{"count":0,"title":"solo"}]`, "title:solo", "count:0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer srv.Close()

			conn := fakeConn("list")
			conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + string(mustJSON(tc.raw)) + `}]}`)
			addTestConnection(t, srv, config.ServerConfig{Name: "svc"}, conn)
			serve(t, srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "list",
				"projection": map[string]any{"format": "mini"},
			}))

			resp := serve(t, srv, callTool("call", map[string]any{
				"server": "svc", "tool": "list", "params": map[string]any{},
			}))
			text := toolResultText(t, resp)
			if !strings.Contains(text, tc.want) {
				t.Errorf("expected %q in mini format output: %s", tc.want, text)
			}
			if strings.Contains(text, tc.notWant) {
				t.Errorf("expected zero value suppressed (no %q) in mini format output: %s", tc.notWant, text)
			}
		})
	}
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

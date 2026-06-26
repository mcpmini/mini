//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/jq"
	"github.com/mcpmini/mini/internal/server"
)

// TestProxy_MiniRead_JQRoundTrip verifies that jq paths in __mini.excluded and
// __mini.truncated can be used directly as read() filters against the raw file.
func TestProxy_MiniRead_JQRoundTrip(t *testing.T) {
	upstream := `{"id":1,"secret":"key-123","body":"` + longString(3000) + `","items":[{"n":1},{"n":2},{"n":3},{"n":4},{"n":5}]}`

	cases := []struct {
		name       string
		projection map[string]any
		wantPaths  func(mini map[string]any) []string
	}{
		{
			name:       "excluded field",
			projection: map[string]any{"exclude": []string{"secret"}},
			wantPaths: func(mini map[string]any) []string {
				exc, _ := mini["excluded"].([]any)
				out := make([]string, len(exc))
				for i, v := range exc {
					out[i], _ = v.(string)
				}
				return out
			},
		},
		{
			name:       "truncated string",
			projection: map[string]any{"string_limit": 100},
			wantPaths:  truncatedPaths,
		},
		{
			name:       "truncated array",
			projection: map[string]any{"array_limit": 2},
			wantPaths:  truncatedPaths,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer srv.Close()

			conn := fakeConn("get_data")
			conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + marshalString(upstream) + `}]}`)
			addProxyConn(t, srv, "svc", conn)

			serveProxy(t, srv, callTool("config", map[string]any{
				"action":     "set_projection",
				"server":     "svc",
				"tool":       "get_data",
				"projection": tc.projection,
			}))

			resp := serveProxy(t, srv, callTool("svc__get_data", map[string]any{}))
			text := toolResultText(t, resp)

			var env map[string]any
			if err := json.Unmarshal([]byte(text), &env); err != nil {
				t.Fatalf("expected JSON envelope, got: %s", text)
			}
			mini, _ := env["__mini"].(map[string]any)
			if mini == nil {
				t.Fatal("expected __mini envelope from projection")
			}
			filePath, _ := mini["file"].(string)
			if filePath == "" {
				t.Fatalf("expected __mini.file, got: %s", text)
			}

			rawFile, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("read raw file: %v", err)
			}

			paths := tc.wantPaths(mini)
			if len(paths) == 0 {
				t.Fatal("no paths found in projection note — check projection config")
			}

			for _, path := range paths {
				want, err := jq.Eval(context.Background(), rawFile, path)
				if err != nil {
					t.Errorf("jq.Eval(%q) on raw file: %v", path, err)
					continue
				}

				resp2 := serveProxy(t, srv, callTool("read", map[string]any{"path": filePath, "filter": path}))
				got := toolResultText(t, resp2)

				if got != want {
					t.Errorf("read(file, %q): got %q, want %q", path, got, want)
				}
			}
		})
	}
}

func truncatedPaths(mini map[string]any) []string {
	trunc, _ := mini["truncated"].([]any)
	out := make([]string, 0, len(trunc))
	for _, v := range trunc {
		m, _ := v.(map[string]any)
		p, _ := m["path"].(string)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(i%26)
	}
	return string(b)
}

func marshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

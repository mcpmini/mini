//go:build test

package server_test

import (
    "encoding/json"
    "strings"
    "testing"
    "io"
    "log/slog"
    "github.com/mcpmini/mini/internal/config"
    "github.com/mcpmini/mini/internal/server"
)

func TestMiniFormat_AllArrayShapes(t *testing.T) {
    cases := []struct{
        name     string
        raw      string
        wantHdr  bool // expect mini format header row
        wantKeys []string
    }{
        {"uniform", `[{"number":1,"state":"open","title":"Bug"},{"number":2,"state":"closed","title":"Feat"}]`,
            true, []string{"number","state","title"}},
        {"non-uniform (diff keys)", `[{"number":1,"title":"Bug","labels":["bug"]},{"number":2,"title":"Feat"}]`,
            false, nil},
        {"single item (no header)", `[{"number":1,"title":"Solo","state":"open"}]`,
            false, nil},
        {"string array", `["alpha","beta","gamma"]`, false, nil},
        {"wrapped map+array", `{"total":3,"items":[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]}`,
            true, []string{"id","name"}},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            cfg := config.DefaultConfig()
            cfg.ResponseDir = t.TempDir()
            cfg.InlineThreshold = 100000
            srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
            defer srv.Close()

            conn := fakeConn("list")
            conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + string(mustJSON(tc.raw)) + `}]}`)
            addProxyConn(t, srv, "svc", conn)
            serveProxy(t, srv, callTool("config", map[string]any{
                "action":"set_projection","server":"svc","tool":"list",
                "projection":map[string]any{"format":"mini"},
            }))

            resp := serveProxy(t, srv, callTool("svc__list", map[string]any{}))
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

func mustJSON(s string) json.RawMessage {
    b, _ := json.Marshal(s)
    return b
}

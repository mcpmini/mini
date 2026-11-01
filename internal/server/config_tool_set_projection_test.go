//go:build test

package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestConfigureSetProjection_aFailedSaveKeepsTheLiveRuleAndSessionOnlyStillApplies(t *testing.T) {
	const broken = "command: echo\nprojections: {getData: {include_only: 5}}\n"
	cases := map[string]func(t *testing.T, path string){
		"broken server file": func(t *testing.T, path string) { testutil.WriteFile(t, path, broken) },
		"missing server file": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"unwritable servers directory": func(t *testing.T, path string) {
			dir := filepath.Dir(path)
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(
				func() { os.Chmod(dir, 0o700) },
			) //nolint:errcheck // TempDir cleanup reports a directory it can't remove
		},
	}
	for name, breakFile := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			addEdgeConn(addConnectionParams{
				T:      t,
				Server: srv,
				Config: config.ServerConfig{Name: "svc", Projections: map[string]*config.ProjectionConfig{
					"getData": {IncludeOnly: []string{"a"}},
				}},
				Connection: fakeGetData(),
			})
			path := config.ServerPath(srv.ConfigDir(), "svc")
			breakFile(t, path)
			before := serverFileText(t, path)
			const sessionID = "cccccccc-cccc-cccc-cccc-000000000004"
			postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg()})

			saved := postMCP(
				postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: setGetDataProjection(2, false)},
			)
			assertIsErrorResult(t, saved)
			if text := toolResultText(t, saved); !strings.Contains(text, "session_only") {
				t.Errorf("failed save = %q, want session_only advice", text)
			}
			assertProjectedFields(
				assertProjectedFieldsParams{
					T: t,
					Text: toolResultText(
						t,
						postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: callGetData(3)}),
					),
					Present: []string{"a"},
					Absent:  []string{"b"},
				},
			)

			sessionOnly := postMCP(
				postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: setGetDataProjection(4, true)},
			)
			if text := toolResultText(t, sessionOnly); strings.Contains(text, `"error"`) {
				t.Fatalf("session_only set_projection = %s", text)
			}
			assertProjectedFields(
				assertProjectedFieldsParams{
					T: t,
					Text: toolResultText(
						t,
						postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: callGetData(5)}),
					),
					Present: []string{"b"},
					Absent:  []string{"a"},
				},
			)
			if after := serverFileText(t, path); after != before {
				t.Errorf("server file = %q, want it untouched (%q)", after, before)
			}
		})
	}
}

func TestConfigureSetProjectionRequiresTool(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "svc",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureSetProjection_rejectsMiniFormat(t *testing.T) {
	t.Run("server scope", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		resp := serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "svc", "tool": "tool1",
			"projection": map[string]any{"format": "mini"},
		}))
		assertIsErrorResult(t, resp)
		if text := toolResultText(t, resp); !strings.Contains(text, "toon") {
			t.Errorf("expected error naming toon as the replacement, got: %s", text)
		}
	})
	t.Run("session scope", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		resp := serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "svc", "tool": "tool1", "session_only": true,
			"projection": map[string]any{"format": "mini"},
		}))
		assertIsErrorResult(t, resp)
		if text := toolResultText(t, resp); !strings.Contains(text, "toon") {
			t.Errorf("expected error naming toon as the replacement, got: %s", text)
		}
	})
}

func TestConfigureSetProjection_rejectsUnknownOption(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "tool1",
		"projection": map[string]any{"exlude": []string{"secret"}},
	}))
	assertIsErrorResult(t, resp)
	if text := toolResultText(t, resp); !strings.Contains(text, `"exlude"`) || !strings.Contains(text, "exclude") {
		t.Errorf("expected an error naming the unknown option and the valid ones, got: %s", text)
	}
}

func TestConfigureProjectionRejectsUnsupportedExcludePathsWithoutPublishing(t *testing.T) {
	for _, scope := range []string{"server", "session"} {
		t.Run(scope, func(t *testing.T) {
			const sessionID = "cccccccc-cccc-cccc-cccc-000000000051"
			previous := &config.ProjectionConfig{Exclude: []string{"head.repo.description", "[].author.avatar_url"}}
			srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": previous})
			args := map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData", "session_only": scope == "session",
				"projection": previous,
			}
			resp := postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: configCall(11, args)})
			if text := toolResultText(t, resp); !strings.Contains(text, `"ok":true`) {
				t.Fatalf("valid projection was rejected: %s", text)
			}
			want := getProjection(getProjectionParams{T: t, Server: srv, SessionID: sessionID, Tool: "getData"})
			file := config.ServerPath(srv.ConfigDir(), "svc")
			before := string(testutil.ReadFile(t, file))
			args["projection"] = &config.ProjectionConfig{Exclude: []string{"number", "items[0].desc"}}
			resp = postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: configCall(12, args)})
			assertIsErrorResult(t, resp)
			text := toolResultText(t, resp)
			if !strings.Contains(text, `invalid exclude path "items[0].desc"`) ||
				!strings.Contains(text, "for example items[].secret") {
				t.Errorf("rejected path needs actionable error: %s", text)
			}
			assertReport(
				t,
				getProjection(getProjectionParams{T: t, Server: srv, SessionID: sessionID, Tool: "getData"}),
				want,
			)
			if after := string(testutil.ReadFile(t, file)); after != before {
				t.Errorf("invalid projection changed persisted rule: %s", after)
			}
		})
	}
}

func fakeGetData() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "getData", Description: "get", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`),
		},
	}
}

func serverFileText(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "<missing>"
	}
	return string(testutil.ReadFile(t, path))
}

func callGetData(id int) map[string]any {
	return map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "svc__getData", "arguments": map[string]any{},
		},
	}
}

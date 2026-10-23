//go:build test

package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestConfigureReload_emptyDir(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, server.Params{ConfigDir: dir})

	resp := serve(t, srv, callTool("config", map[string]any{"action": "reload"}))
	text := toolResultText(t, resp)

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON from reload, got: %s", text)
	}
	if result["error"] != nil {
		t.Errorf("expected ok=true from reload, got: %v", result)
	}
}

func TestConfigureReload_loadsProjectionsFromDisk(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "myserver", Command: "echo"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "myserver",
		Tools: map[string]*config.ProjectionConfig{"search": {
			StringLimits: map[string]int{"body": 50},
		}},
	})

	srv := newTestServer(t, server.Params{ConfigDir: dir})

	resp := serve(t, srv, callTool("config", map[string]any{"action": "reload"}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result)
	if result["error"] != nil {
		t.Errorf("expected ok=true from reload, got: %v", result)
	}
}

func TestConfigureAddServer_noConfig(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{"action": "add_server"}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true when config omitted, got: %v", result)
	}
}

func newServerAllowPrivate(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	return newTestServer(t, server.Params{Config: cfg})
}

func TestConfigureAddServer_viaHTTP(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newServerAllowPrivate(t)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
	}))
	var result map[string]any
	json.Unmarshal([]byte(resultJSONText(t, resp)), &result)
	if result["error"] != nil || result["server"] != "dynamic" {
		t.Errorf("expected ok add_server, got: %v", result)
	}

	listText := resultJSONText(t, serve(t, srv, rpc("tools/list", nil)))
	var tools []any
	json.Unmarshal([]byte(listText), &tools)
	if len(tools) != 3 {
		t.Errorf("expected native tool and two builtins after add_server, got %d: %s", len(tools), listText)
	}
}

func fakeProtectedConn() (*transport.FakeConnection, *config.PermissionsConfig) {
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "deleteAll", Description: "delete everything", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"deleted"}]}`),
		},
	}
	return fake, &config.PermissionsConfig{Protected: []string{"deleteAll"}}
}

func TestExecuteProtected_callsProtectedTool(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake, perm := fakeProtectedConn()
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "db", Permissions: perm}, fake)

	resp := serve(t, srv, callTool("db__deleteAll", map[string]any{}))
	if text := toolResultText(t, resp); text != "deleted" {
		t.Fatalf("protected tool result = %q, want deleted", text)
	}
}

func newConfigServer(t *testing.T) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)
	return srv
}

func execGetFileInfo(t *testing.T, srv *server.Server) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("fs__get_file_info", map[string]any{}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("get_file_info failed: %v", env)
	}
	summary := env
	if summary == nil {
		t.Fatalf("nil summary: %v", env)
	}
	return summary
}

func assertExcluded(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if obj[k] != nil {
			t.Errorf("field %q should be excluded, got: %v", k, obj[k])
		}
	}
}

func TestProjectionExcludeFields(t *testing.T) {
	srv := newConfigServer(t)
	payload := `{"name":"test.txt","size":5,"created":"2026-01-01","permissions":"644","isDirectory":false}`
	payloadJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "get_file_info", Description: "info", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`),
		},
	}
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "fs"}, Connection: fake})
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "get_file_info",
		"projection": map[string]any{"include_only": []string{"name", "size"}},
	}))
	summary := execGetFileInfo(t, srv)
	if summary["name"] == nil {
		t.Error("expected 'name' in projected summary")
	}
	assertExcluded(t, summary, "created", "permissions", "isDirectory")
}

func TestProjectionTruncation_fieldNameAndChars(t *testing.T) {
	srv := newConfigServer(t)
	longBody := strings.Repeat("z", 300)
	payload := `{"id":1,"title":"short","body":"` + longBody + `"}`
	payloadJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "get_doc", Description: "doc", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`),
		},
	}
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fake})
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_doc",
		"projection": map[string]any{"string_limits": map[string]any{"body": 50}},
	}))

	resp := serve(t, srv, callTool("svc__get_doc", nil))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("get_doc failed: %v", env)
	}

	notice := env["__mini"].(map[string]any)
	truncated, _ := notice["truncated"].([]any)
	if len(truncated) == 0 {
		t.Errorf("expected truncated entries, got %v", env["truncated"])
	}
	var bodyChars float64
	for _, o := range truncated {
		om, _ := o.(map[string]any)
		if om["path"] == ".content[0].text | fromjson | .body" {
			bodyChars, _ = om["chars"].(float64)
		}
	}
	if bodyChars <= 0 {
		t.Errorf("expected truncated[body].chars > 0, got truncated=%v", truncated)
	}
	// body had 300 chars, limit 50 → removed ≥ 200
	if int(bodyChars) < 200 {
		t.Errorf("expected at least 200 chars removed from body, got %v", bodyChars)
	}
	if notice["file"] == nil {
		t.Error("expected file to be written when truncation occurred")
	}
}

type assertHealthStatsParams struct {
	T          *testing.T
	Server     *server.Server
	ServerName string
	WantCalls  int
}

func assertHealthStats(params assertHealthStatsParams) {
	params.T.Helper()
	var status map[string]any
	json.Unmarshal(
		[]byte(
			toolResultText(
				params.T,
				serve(params.T, params.Server, callTool("config", map[string]any{"action": "status"})),
			),
		),
		&status,
	)
	servers, _ := status["servers"].(map[string]any)
	svc, _ := servers[params.ServerName].(map[string]any)
	if calls, _ := svc["calls"].(float64); int(calls) != params.WantCalls {
		params.T.Errorf("expected %d calls, got %v", params.WantCalls, calls)
	}
	if svc["last_call"] == nil {
		params.T.Error("expected last_call timestamp")
	}
	lastCall, _ := svc["last_call"].(string)
	if _, err := time.Parse(time.RFC3339, lastCall); err != nil {
		params.T.Errorf("last_call not RFC3339: %q", lastCall)
	}
}

func TestHealthStatsAfterCalls(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)
	const nCalls = 3
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`),
		},
	}
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fake})
	for i := 0; i < nCalls; i++ {
		serve(t, srv, callTool("svc__ping", map[string]any{}))
	}
	assertHealthStats(assertHealthStatsParams{T: t, Server: srv, ServerName: "svc", WantCalls: nCalls})
}

func TestHealthStats_upstreamToolErrorRecorded(t *testing.T) {
	failing := func() *transport.FakeConnection {
		conn := fakeConn("ping")
		conn.Responses["tools/call"] = json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)
		return conn
	}

	t.Run("proxy mode", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		t.Cleanup(srv.Close)
		addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: failing()})
		resp := serveProxy(t, srv, callTool("svc__ping", map[string]any{}))
		assertIsErrorResult(t, resp)
		assertFailedCallRecorded(
			assertFailedCallRecordedParams{
				T:             t,
				Server:        srv,
				ServerName:    "svc",
				WantLastError: "tool returned error: boom",
			},
		)
	})
}

type assertFailedCallRecordedParams struct {
	T             *testing.T
	Server        *server.Server
	ServerName    string
	WantLastError string
}

func assertFailedCallRecorded(params assertFailedCallRecordedParams) {
	params.T.Helper()
	var status map[string]any
	if err := json.Unmarshal(
		[]byte(
			toolResultText(
				params.T,
				serve(params.T, params.Server, callTool("config", map[string]any{"action": "status"})),
			),
		),
		&status,
	); err != nil {
		params.T.Fatalf("status is not JSON: %v", err)
	}
	servers, _ := status["servers"].(map[string]any)
	svc, _ := servers[params.ServerName].(map[string]any)
	if errs, _ := svc["errors"].(float64); errs != 1 {
		params.T.Errorf("errors = %v, want 1", svc["errors"])
	}
	if svc["last_error"] != params.WantLastError {
		params.T.Errorf("last_error = %v, want %q", svc["last_error"], params.WantLastError)
	}
}

func newSessionServer(t *testing.T) *server.Server {
	t.Helper()
	return newTestServer(t, server.Params{})
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

func assertRemoveOk(t *testing.T, srv *server.Server, serverName string) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": serverName,
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result: %s", text)
	}
	if result["error"] != nil {
		t.Errorf("expected ok=true, got: %v", result)
	}
}

func TestConfigureUnknownAction(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{"action": "no_such_action"}))
	assertIsErrorResult(t, resp)
	text := toolResultText(t, resp)
	if !strings.Contains(text, "unknown configure action") {
		t.Errorf("expected error message, got: %s", text)
	}
}

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

func serverFileText(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "<missing>"
	}
	return string(testutil.ReadFile(t, path))
}

func setGetDataProjection(id int, sessionOnly bool) map[string]any {
	return map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "config", "arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData", "session_only": sessionOnly,
				"projection": map[string]any{"include_only": []string{"b"}},
			},
		},
	}
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

type assertProjectedFieldsParams struct {
	T       *testing.T
	Text    string
	Present []string
	Absent  []string
}

func assertProjectedFields(params assertProjectedFieldsParams) {
	params.T.Helper()
	data := parseProxyEnvelope(params.T, params.Text).Data
	for _, key := range params.Present {
		if data[key] == nil {
			params.T.Errorf("field %q missing from projected data %v", key, data)
		}
	}
	for _, key := range params.Absent {
		if data[key] != nil {
			params.T.Errorf("field %q survived projection in %v", key, data)
		}
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

func TestConfigureRemoveServerRequiresName(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureRemoveServer_clearsDiscover(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	addEdgeConn(
		addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fakeConn("ping")},
	)
	if srv.ToolCount("svc") != 1 {
		t.Fatalf("expected 1 tool before remove")
	}
	assertRemoveOk(t, srv, "svc")

	discoverText := resultJSONText(t, serve(t, srv, rpc("tools/list", nil)))
	if strings.Contains(discoverText, "ping") {
		t.Errorf("removed server's tools should not appear in discover, got: %s", discoverText)
	}
}

func TestSessionScopedProjectionNotPersistedAcrossCalls(t *testing.T) {
	srv := newSessionServer(t)
	addEdgeConn(
		addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fakeGetData()},
	)

	serve(t, srv, callTool("config", map[string]any{
		"action":       "set_projection",
		"server":       "svc",
		"tool":         "getData",
		"projection":   map[string]any{"include_only": []string{"a"}},
		"session_only": true,
	}))

	resp := serve(t, srv, callTool("svc__getData", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.Contains(text, `"b":2`) {
		t.Logf("note: session projection applied within same session: %s", text)
	}
}

func reloadResult(t *testing.T, dir string, editsAfterStart map[string]string) map[string]any {
	t.Helper()
	srv := newTestServer(t, server.Params{ConfigDir: dir})
	t.Cleanup(srv.Close)
	for rel, content := range editsAfterStart {
		testutil.WriteFile(t, filepath.Join(dir, rel), content)
	}
	var result map[string]any
	if err := json.Unmarshal(
		[]byte(toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "reload"})))),
		&result,
	); err != nil {
		t.Fatalf("expected JSON from reload: %v", err)
	}
	return result
}

func TestConfigureReload_resultShape(t *testing.T) {
	cases := []struct {
		name             string
		servers          []config.ServerConfig
		files            map[string]string
		editsAfterStart  map[string]string
		wantOK           bool
		wantLoaded       []string
		wantNotLoaded    []string
		wantSourceErrors bool
		wantErrorFile    string
	}{
		{
			name: "clean reload: ok=true, loaded counts, no source_errors",
			servers: []config.ServerConfig{
				{
					Name:    "a",
					Command: "echo",
					Projections: map[string]*config.ProjectionConfig{
						"t": {
							IncludeOnly: []string{"x"},
						},
					},
				},
			},
			wantOK:     true,
			wantLoaded: []string{"a"},
		},
		{
			name:             "broken server file: ok=false, source_errors present",
			files:            map[string]string{"servers/a.yaml": "bad: [yaml\n"},
			wantSourceErrors: true,
		},
		{
			name:    "bad inline projection: ok=false, source_errors names the server file",
			servers: []config.ServerConfig{{Name: "a", Command: "echo"}},
			files: map[string]string{
				"servers/a.yaml": "command: echo\nprojections: {tool: {format: invalid}}\n",
			},
			wantSourceErrors: true,
			wantErrorFile:    "a.yaml",
		},
		{
			name: "loaded excludes kept-previous server when its file broke",
			servers: []config.ServerConfig{
				{
					Name:    "a",
					Command: "echo",
					Projections: map[string]*config.ProjectionConfig{
						"t": {
							IncludeOnly: []string{"x"},
						},
					},
				},
				{
					Name:    "b",
					Command: "echo",
					Projections: map[string]*config.ProjectionConfig{
						"t": {
							IncludeOnly: []string{"y"},
						},
					},
				},
			},
			editsAfterStart:  map[string]string{"servers/b.yaml": "bad: [yaml\n"},
			wantLoaded:       []string{"a"},
			wantNotLoaded:    []string{"b"},
			wantSourceErrors: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := evalTempDir(t)
			for _, server := range tc.servers {
				configtest.WriteServer(t, dir, server)
			}
			for rel, content := range tc.files {
				testutil.WriteFile(t, filepath.Join(dir, rel), content)
			}
			result := reloadResult(t, dir, tc.editsAfterStart)
			if result["ok"] != tc.wantOK {
				t.Errorf("ok: got %v, want %v", result["ok"], tc.wantOK)
			}
			if !tc.wantSourceErrors && result["source_errors"] != nil {
				t.Errorf("expected no source_errors, got %v", result["source_errors"])
			}
			if tc.wantSourceErrors {
				errs, _ := result["source_errors"].([]any)
				if len(errs) == 0 {
					t.Fatalf("expected source_errors list, got %v", result)
				}
				if path, _ := errs[0].(string); tc.wantErrorFile != "" && filepath.Base(path) != tc.wantErrorFile {
					t.Errorf("source_errors = %v, want it to name %s", errs, tc.wantErrorFile)
				}
			}
			loaded, _ := result["loaded"].(map[string]any)
			for _, name := range tc.wantLoaded {
				if loaded[name] == nil {
					t.Errorf("loaded must include %q, got %v", name, loaded)
				}
			}
			for _, name := range tc.wantNotLoaded {
				if loaded[name] != nil {
					t.Errorf("loaded must not include %q, got %v", name, loaded)
				}
			}
		})
	}
}

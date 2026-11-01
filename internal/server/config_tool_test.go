//go:build test

package server_test

import (
	"context"
	"encoding/json"
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

func newConfigServer(t *testing.T) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)
	return srv
}

func execGetFileInfo(t *testing.T, srv *server.Server) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("fs__get_file_info", map[string]any{}))
	return parseEnvelope(t, successText(t, resp))
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
	env := parseEnvelope(t, successText(t, resp))

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
		resp := serve(t, srv, callTool("svc__ping", map[string]any{}))
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

func TestConfigureUnknownAction(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{"action": "no_such_action"}))
	assertIsErrorResult(t, resp)
	text := toolResultText(t, resp)
	if !strings.Contains(text, "unknown configure action") {
		t.Errorf("expected error message, got: %s", text)
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

func TestConfigureStatus(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "ci"}, fakeConn("getBuild", "listBuilds"))

	resp := serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, text)
	}
	if status["servers"] == nil {
		t.Error("expected servers in status")
	}
}

//go:build test

package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

type missingToolError struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action"`
}

type toolSurface struct {
	name string
	call func(t *testing.T, srv *server.Server) map[string]any
}

var missingToolSurfaces = []toolSurface{
	{name: "call", call: func(t *testing.T, srv *server.Server) map[string]any {
		return serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "ping"}))
	}},
	{name: "perm_call", call: func(t *testing.T, srv *server.Server) map[string]any {
		return serve(t, srv, callTool("perm_call", map[string]any{"server": "svc", "tool": "ping"}))
	}},
	{name: "list detail", call: func(t *testing.T, srv *server.Server) map[string]any {
		return serve(t, srv, callTool("list", map[string]any{"tool": "svc.ping", "detail": true}))
	}},
	{name: "proxy call", call: func(t *testing.T, srv *server.Server) map[string]any {
		return serveProxy(t, srv, callTool("svc__ping", map[string]any{}))
	}},
}

func missingToolErrorOf(t *testing.T, resp map[string]any) missingToolError {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Fatalf("want a tool error result the model reads, got %v", resp)
	}
	var got missingToolError
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &got); err != nil {
		t.Fatalf("tool error isn't a JSON envelope: %v\n%s", err, toolResultText(t, resp))
	}
	return got
}

func TestMissingTool_aConfiguredServerThatIsNotConnectedSaysWhy(t *testing.T) {
	needsAuth := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(needsAuth.Close)
	cases := []struct {
		name          string
		start         func(t *testing.T) startupRetry
		want          string
		wantRetryable bool
		wantAction    string
	}{
		{name: "connecting", want: "server_starting", wantRetryable: true, start: func(t *testing.T) startupRetry {
			url, _ := gatedUpstream(t)
			return connectWithFakeClock(t, httpServer("svc", url))
		}},
		{name: "delayed", want: "server_delayed", wantRetryable: true, start: func(t *testing.T) startupRetry {
			url, _ := gatedUpstream(t)
			r := connectWithFakeClock(t, httpServer("svc", url))
			r.clock.Advance(startupHold)
			return r
		}},
		{
			name:       "needs authorization",
			want:       "server_needs_auth",
			wantAction: "start_auth",
			start: func(t *testing.T) startupRetry {
				return settledStartup(t, httpServer("svc", needsAuth.URL))
			},
		},
		{name: "environment variables unset", want: "server_needs_env", start: func(t *testing.T) startupRetry {
			return settledStartup(t, config.ServerConfig{
				Name: "svc", Transport: "http", URL: needsAuth.URL,
				UnsetEnv: &config.UnsetEnvError{Field: "headers.Authorization", Names: []string{"MINI_TEST_UNSET"}},
			})
		}},
		{name: "command an agent added", want: "server_not_trusted", start: func(t *testing.T) startupRetry {
			return settledStartup(t, config.ServerConfig{Name: "svc", Command: "true", AgentAdded: true})
		}},
	}
	for _, tc := range cases {
		for _, surface := range missingToolSurfaces {
			t.Run(tc.name+"/"+surface.name, func(t *testing.T) {
				r := tc.start(t)

				got := missingToolErrorOf(t, surface.call(t, r.srv))

				if got.Error != tc.want || got.Retryable != tc.wantRetryable || got.Action != tc.wantAction {
					t.Errorf("error = %+v, want error %q, retryable %v, action %q",
						got, tc.want, tc.wantRetryable, tc.wantAction)
				}
				if !strings.Contains(got.Message, `"svc"`) {
					t.Errorf("message %q doesn't name the server", got.Message)
				}
			})
		}
	}
}

func settledStartup(t *testing.T, sc config.ServerConfig) startupRetry {
	t.Helper()
	r := connectWithFakeClock(t, sc)
	r.srv.WaitForStartupConnects()
	return r
}

func TestMissingTool_staysNotFoundWhenTheServerCanNotExplainIt(t *testing.T) {
	cases := []struct {
		name   string
		server string
	}{
		{name: "server isn't configured", server: "nowhere"},
		{name: "connected server has no such tool", server: "ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := settledStartup(t, httpServer("ready", newMCPTestServer(t, pingTools).URL)).srv

			compact := serve(t, srv, callTool("call", map[string]any{"server": tc.server, "tool": "missing"}))
			if got := missingToolErrorOf(t, compact); got.Error != "not_found" {
				t.Errorf("compact call error = %+v, want not_found", got)
			}
			proxy := serveProxy(t, srv, callTool(tc.server+"__missing", map[string]any{}))
			if code := rpcErrorCode(proxy); code != -32602 {
				t.Errorf("proxy call = %v, want JSON-RPC invalid params", proxy)
			}
		})
	}
}

func rpcErrorCode(resp map[string]any) float64 {
	errVal, _ := resp["error"].(map[string]any)
	code, _ := errVal["code"].(float64)
	return code
}

func TestMissingTool_aNotReadyErrorFollowsTheConfiguredResponseFormatOnEveryCompactSurface(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	cfg.ResponseFormat = config.FormatToon
	srv := newTestServer(t, server.Params{Config: cfg, Logger: slog.New(discardLogs()), Clock: clock.NewFake()})
	url, _ := gatedUpstream(t)
	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{httpServer("svc", url)})

	for _, surface := range missingToolSurfaces {
		if surface.name == "proxy call" {
			continue
		}
		text := toolResultText(t, surface.call(t, srv))
		if strings.HasPrefix(strings.TrimSpace(text), "{") || !strings.Contains(text, "server_starting") {
			t.Errorf("%s returned %q, want a TOON server_starting error", surface.name, text)
		}
	}
}

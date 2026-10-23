//go:build test

package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

type missingToolError struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action"`
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
		t.Run(tc.name, func(t *testing.T) {
			r := tc.start(t)

			got := missingToolErrorOf(
				t,
				serve(t, r.srv, callTool("svc__ping", nil)),
			)

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

func settledStartup(t *testing.T, sc config.ServerConfig) startupRetry {
	t.Helper()
	r := connectWithFakeClock(t, sc)
	r.srv.WaitForStartupConnects()
	return r
}

func TestMissingTool_isInvalidParamsWhenTheServerCanNotExplainIt(t *testing.T) {
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

			resp := serve(t, srv, callTool(tc.server+"__missing", map[string]any{}))
			if code := rpcErrorCode(resp); code != -32602 {
				t.Errorf("call = %v, want JSON-RPC invalid params", resp)
			}
		})
	}
}

func rpcErrorCode(resp map[string]any) float64 {
	errVal, _ := resp["error"].(map[string]any)
	code, _ := errVal["code"].(float64)
	return code
}

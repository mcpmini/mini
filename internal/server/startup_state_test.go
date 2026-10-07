//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const startupHold = 10 * time.Second

type startupStatus struct {
	Servers     map[string]json.RawMessage `json:"servers"`
	Starting    []string                   `json:"starting"`
	Unavailable map[string]struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	} `json:"unavailable"`
	raw string
}

func statusOf(t *testing.T, srv *server.Server) startupStatus {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
	status := startupStatus{raw: text}
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status: %v\n%s", err, text)
	}
	return status
}

func (st startupStatus) stateOf(name string) string {
	switch {
	case st.Servers[name] != nil:
		return "connected"
	case slices.Contains(st.Starting, name):
		return "connecting"
	}
	if u, ok := st.Unavailable[name]; ok {
		return u.State
	}
	return "not configured"
}

func connectWithFakeClock(t *testing.T, servers ...config.ServerConfig) startupRetry {
	t.Helper()
	r := startupRetry{clock: clock.NewFake()}
	r.srv = newConnectTestServerLogging(t, discardLogs(), r.clock)
	t.Cleanup(r.srv.Close)
	r.srv.ConnectUpstreams(context.Background(), servers)
	return r
}

func httpServer(name, url string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url}
}

func alwaysFailingUpstream(t *testing.T) (url string) {
	t.Helper()
	ts, _ := upstreamFailingFirst(t, 1<<30, pingMCPHandler)
	return ts.URL
}

func TestConfigStatus_aServerFailingThenConnectingInsideItsWindowIsStartingUntilItsToolsAreReady(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, pingMCPHandler)
	r := connectWithFakeClock(t, httpServer("svc", ts.URL))

	r.waitForBackoffTimer(t)
	if got := statusOf(t, r.srv).stateOf("svc"); got != "connecting" {
		t.Errorf("after a retryable failure inside the window, svc is %s, want connecting", got)
	}
	r.clock.Advance(time.Second)
	r.srv.WaitForStartupConnects()

	status := statusOf(t, r.srv)
	if got := status.stateOf("svc"); got != "connected" || len(status.Starting) != 0 {
		t.Errorf(
			"after connecting, svc is %s and starting is %v, want connected and nothing starting",
			got,
			status.Starting,
		)
	}
}

func TestConfigStatus_aServerStillUnconnectedWhenItsWindowEndsIsDelayed(t *testing.T) {
	cases := []struct {
		name        string
		upstreamURL func(*testing.T) string
	}{
		{name: "first attempt still running", upstreamURL: func(t *testing.T) string {
			url, _ := gatedUpstream(t)
			return url
		}},
		{name: "retrying after failures", upstreamURL: alwaysFailingUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := connectWithFakeClock(t, httpServer("svc", tc.upstreamURL(t)))
			if got := statusOf(t, r.srv).stateOf("svc"); got != "connecting" {
				t.Fatalf("inside its window svc is %s, want connecting", got)
			}

			r.clock.Advance(startupHold)

			status := statusOf(t, r.srv)
			if got := status.stateOf("svc"); got != "delayed" {
				t.Fatalf("after its window svc is %s, want delayed", got)
			}
			if reason := status.Unavailable["svc"].Reason; !strings.Contains(reason, "mini status") {
				t.Errorf("delayed reason %q doesn't tell the agent how the user can see why", reason)
			}
		})
	}
}

func TestConfigStatus_aFailureARetryCantFixIsReportedWithItsRemedy(t *testing.T) {
	needsAuth := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(needsAuth.Close)
	cases := []struct {
		name       string
		server     config.ServerConfig
		wantReason []string
	}{
		{
			name:       "needs authorization",
			server:     httpServer("svc", needsAuth.URL),
			wantReason: []string{`"start_auth"`},
		},
		{
			name: "environment variables unset",
			server: config.ServerConfig{
				Name:      "svc",
				Transport: "http",
				URL:       needsAuth.URL,
				UnsetEnv: &config.UnsetEnvError{
					Field: "headers.Authorization",
					Names: []string{"MINI_TEST_UNSET_A", "MINI_TEST_UNSET_B"},
				},
			},
			wantReason: []string{"MINI_TEST_UNSET_A, MINI_TEST_UNSET_B", "starts again"},
		},
		{
			name:       "command an agent added",
			server:     config.ServerConfig{Name: "svc", Command: "true", AgentAdded: true},
			wantReason: []string{"agent_added", "starts again"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := connectWithFakeClock(t, tc.server)
			r.srv.WaitForStartupConnects()

			status := statusOf(t, r.srv)
			if got := status.stateOf("svc"); got != "failed" {
				t.Fatalf("svc is %s, want failed:\n%s", got, status.raw)
			}
			for _, want := range tc.wantReason {
				if reason := status.Unavailable["svc"].Reason; !strings.Contains(reason, want) {
					t.Errorf("reason %q doesn't contain %q", reason, want)
				}
			}
		})
	}
}

func TestConfigStatus_aFailedServerThatConnectsIsConnectedOnly(t *testing.T) {
	needsAuth := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(needsAuth.Close)
	r := connectWithFakeClock(t, httpServer("svc", needsAuth.URL))
	r.srv.WaitForStartupConnects()

	if err := r.srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "svc"},
		fakeConn("ping"),
	); err != nil {
		t.Fatal(err)
	}

	status := statusOf(t, r.srv)
	if _, stillFailed := status.Unavailable["svc"]; stillFailed || status.stateOf("svc") != "connected" {
		t.Errorf("after connecting, svc should be listed as connected only:\n%s", status.raw)
	}
}

func TestConfigStatus_aServerRemovedDuringStartupIsNotListed(t *testing.T) {
	r := connectWithFakeClock(t, httpServer("svc", alwaysFailingUpstream(t)))
	r.waitForBackoffTimer(t)

	serve(t, r.srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
	r.clock.Advance(startupHold)

	if got := statusOf(t, r.srv).stateOf("svc"); got != "not configured" {
		t.Errorf("removed svc is %s, want not configured", got)
	}
}

func TestConfigStatus_aStartupFailureReasonNeverCarriesTheUpstreamError(t *testing.T) {
	needsAuth := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(needsAuth.Close)
	r := connectWithFakeClock(t, httpServer("svc", needsAuth.URL+"/mcp?token=s3cret"))
	r.srv.WaitForStartupConnects()

	if status := statusOf(t, r.srv); status.stateOf("svc") != "failed" || strings.Contains(status.raw, "s3cret") {
		t.Errorf("status should report svc failed without its URL's credential:\n%s", status.raw)
	}
}

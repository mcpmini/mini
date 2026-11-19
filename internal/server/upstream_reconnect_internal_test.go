//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func newInternalTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(srv.Close)
	return srv
}

func registeredToolNames(srv *Server) []string {
	var names []string
	for _, e := range srv.reg.AllFull() {
		names = append(names, e.FullName)
	}
	slices.Sort(names)
	return names
}

func TestAdoptConn_aStoppedIncarnationLeavesTheRegistryAlone(t *testing.T) {
	oldTools := []transport.ToolDefinition{{Name: "old_tool", InputSchema: json.RawMessage(`{}`)}}
	newTools := []transport.ToolDefinition{{Name: "new_tool", InputSchema: json.RawMessage(`{}`)}}

	t.Run("server removed before the reconnect publishes its tools", func(t *testing.T) {
		srv := newInternalTestServer(t)
		srv.AddConnection(
			context.Background(),
			config.ServerConfig{Name: "svc"},
			&transport.FakeConnection{Tools: oldTools},
		)
		reconnecting := srv.snapshotUpstreams()[0]

		if _, err := srv.removeServerFromAgent("svc"); err != nil {
			t.Fatal(err)
		}
		srv.adoptConn(reconnecting, &transport.FakeConnection{}, transport.ToolList{Tools: newTools})

		if got := registeredToolNames(srv); len(got) != 0 {
			t.Errorf("registry after removal = %v, want empty", got)
		}
	})

	t.Run("server replaced by a newer install", func(t *testing.T) {
		srv := newInternalTestServer(t)
		srv.AddConnection(
			context.Background(),
			config.ServerConfig{Name: "svc"},
			&transport.FakeConnection{Tools: oldTools},
		)
		reconnecting := srv.snapshotUpstreams()[0]

		srv.AddConnection(
			context.Background(),
			config.ServerConfig{Name: "svc"},
			&transport.FakeConnection{Tools: newTools},
		)
		srv.adoptConn(reconnecting, &transport.FakeConnection{}, transport.ToolList{Tools: oldTools})

		if got := registeredToolNames(srv); !slices.Equal(got, []string{"svc.new_tool"}) {
			t.Errorf("registry = %v, want only the newer install's svc.new_tool", got)
		}
	})
}

func TestAdoptConn_aReconnectUsesReloadedAliases(t *testing.T) {
	srv := newInternalTestServer(t)

	tools := []transport.ToolDefinition{{Name: "list_pull_requests", InputSchema: json.RawMessage(`{}`)}}
	proj := map[string]*config.ProjectionConfig{"list_pull_requests": {Alias: "old_alias"}}
	srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "gh", Projections: proj},
		&transport.FakeConnection{Tools: tools},
	)

	srv.replaceProjections(map[string]map[string]*config.ProjectionConfig{
		"gh": {"list_pull_requests": {Alias: "new_alias"}},
	}, config.Servers{})

	upstreams := srv.snapshotUpstreams()
	if len(upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(upstreams))
	}
	srv.adoptConn(upstreams[0], &transport.FakeConnection{}, transport.ToolList{Tools: tools})

	names := map[string]bool{}
	for _, e := range srv.reg.AllFull() {
		names[e.FullName] = true
	}
	if !names["gh.new_alias"] {
		t.Errorf("reconnect should use the reloaded alias, got: %v", names)
	}
	if names["gh.old_alias"] {
		t.Errorf("reconnect should not revert to the install-time alias, got: %v", names)
	}
}

func TestNextBackoffDoublesUpTo64Seconds(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{time.Second, 2 * time.Second},
		{32 * time.Second, 64 * time.Second},
		{64 * time.Second, 64 * time.Second},
		{128 * time.Second, 128 * time.Second},
	} {
		if got := nextBackoff(c.in); got != c.want {
			t.Errorf("nextBackoff(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClassifyCallError(t *testing.T) {
	transportErr := errors.New("broken pipe")
	tests := []struct {
		name         string
		err          error
		wantConnFail bool
	}{
		{
			name: "a deadline is the caller's, not the connection's",
			err:  fmt.Errorf("call: %w", context.DeadlineExceeded),
		},
		{name: "a cancellation is the caller's", err: context.Canceled},
		{name: "an RPC error is an answer", err: &transport.RPCError{Code: -32602, Message: "bad args"}},
		{name: "anything else is the connection's", err: transportErr, wantConnFail: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCallError(tc.err)
			if isConnError(got) != tc.wantConnFail {
				t.Errorf("isConnError(classifyCallError(%v)) = %v, want %v", tc.err, !tc.wantConnFail, tc.wantConnFail)
			}
			if !errors.Is(got, tc.err) {
				t.Errorf("classifyCallError(%v) = %v, which no longer wraps the call's error", tc.err, got)
			}
		})
	}
}

func TestMaybeReconnect_afterAFailureARetryCantFix_opensNothing(t *testing.T) {
	srv := newInternalTestServer(t)
	if err := srv.AddConnection(
		t.Context(),
		config.ServerConfig{Name: "svc"},
		&transport.FakeConnection{},
	); err != nil {
		t.Fatal(err)
	}
	u := srv.upstreams.get("svc")
	srv.recordFailure(u, needsAuthStatus{})

	srv.maybeReconnect(u, markConnError(errors.New("broken pipe")))

	if u.isOpening() {
		t.Error("a call failing on svc's old connection reopened it while it waits for a login")
	}
}

func TestMaybeReconnect_toAConfiguredServerThatNowRefusesItsLogin_givesCallsTheLoginRemedy(t *testing.T) {
	srv, fc := newRetryTestServer(t)
	refusing := testutil.Serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	})
	u := srv.newUpstreamServer(config.ServerConfig{
		Name: "svc", Transport: "http", URL: refusing.URL,
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "c", TokenURL: refusing.URL + "/token"},
	}, upstreamOrigin{fromConfig: true})
	srv.upstreams.swap("svc", u)
	srv.adoptConn(u, &transport.FakeConnection{}, transport.ToolList{Tools: []transport.ToolDefinition{{Name: "ping"}}})

	srv.maybeReconnect(u, connError{errors.New("connection reset")})
	if err := fc.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal("the reconnect never waited out its backoff:", err)
	}
	fc.Advance(time.Second)
	srv.WaitForStartupConnects()

	_, err := srv.lookupTool("svc", "ping")
	var notReady errServerNotReady
	if !errors.As(err, &notReady) || notReady.env.Error != "server_needs_auth" {
		t.Errorf(
			"a call after the reconnect was refused = %v, want server_needs_auth so the agent can start a login",
			err,
		)
	}
}

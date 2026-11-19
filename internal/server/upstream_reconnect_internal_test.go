//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
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

func TestPublishReconnectedTools_staleUpstreamLeavesTheRegistryAlone(t *testing.T) {
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
		if srv.publishReconnectedTools(reconnecting, reconnecting.conn, transport.ToolList{Tools: newTools}) {
			t.Error("publish reported success for a removed upstream")
		}
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
		if srv.publishReconnectedTools(reconnecting, reconnecting.conn, transport.ToolList{Tools: oldTools}) {
			t.Error("publish reported success for a replaced upstream")
		}
		if got := registeredToolNames(srv); !slices.Equal(got, []string{"svc.new_tool"}) {
			t.Errorf("registry = %v, want only the newer install's svc.new_tool", got)
		}
	})
}

func TestPublishReconnectedTools_usesReloadedAliases(t *testing.T) {
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
	srv.publishReconnectedTools(upstreams[0], upstreams[0].conn, transport.ToolList{Tools: tools})

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

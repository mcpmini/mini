//go:build test

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

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
	for _, e := range srv.reg.All() {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	return names
}

func TestPublishReconnectedTools_staleUpstreamLeavesTheRegistryAlone(t *testing.T) {
	oldTools := []transport.ToolDefinition{{Name: "old_tool", InputSchema: json.RawMessage(`{}`)}}
	newTools := []transport.ToolDefinition{{Name: "new_tool", InputSchema: json.RawMessage(`{}`)}}

	t.Run("server removed before the reconnect publishes its tools", func(t *testing.T) {
		srv := newInternalTestServer(t)
		srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{Tools: oldTools})
		reconnecting := srv.snapshotUpstreams()[0]

		if _, err := srv.removeServerFromAgent("svc"); err != nil {
			t.Fatal(err)
		}
		if srv.publishReconnectedTools(reconnecting, reconnecting.conn, newTools) {
			t.Error("publish reported success for a removed upstream")
		}
		if got := registeredToolNames(srv); len(got) != 0 {
			t.Errorf("registry after removal = %v, want empty", got)
		}
	})

	t.Run("server replaced by a newer install", func(t *testing.T) {
		srv := newInternalTestServer(t)
		srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{Tools: oldTools})
		reconnecting := srv.snapshotUpstreams()[0]

		srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{Tools: newTools})
		if srv.publishReconnectedTools(reconnecting, reconnecting.conn, oldTools) {
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
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, &transport.FakeConnection{Tools: tools})

	srv.replaceProjections(config.LoadProjectionsResult{
		Projections: map[string]map[string]*config.ProjectionConfig{
			"gh": {"list_pull_requests": {Alias: "new_alias"}},
		},
	})

	upstreams := srv.snapshotUpstreams()
	if len(upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(upstreams))
	}
	srv.publishReconnectedTools(upstreams[0], upstreams[0].conn, tools)

	names := map[string]bool{}
	for _, e := range srv.reg.All() {
		names[e.Name] = true
	}
	if !names["gh.new_alias"] {
		t.Errorf("reconnect should use the reloaded alias, got: %v", names)
	}
	if names["gh.old_alias"] {
		t.Errorf("reconnect should not revert to the install-time alias, got: %v", names)
	}
}

func TestMarkOAuthIfRequired_secondCallSkipsRewrite(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	configDir := t.TempDir()
	srv := New(Params{Config: cfg, ConfigDir: configDir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(srv.Close)

	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: "https://example.com/mcp"}
	connErr := fmt.Errorf("list tools: %w", &transport.UnauthorizedError{WWWAuthenticate: "Bearer"})

	if err := srv.markOAuthIfRequired(context.Background(), sc, connErr); err == nil || !strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Fatalf("first call: err = %v, want oauth-required error", err)
	}

	// Make a rewrite attempt fail: without the fix, the second call would try
	// MarkOAuthDetected again, hit this permission error, and fall back to returning
	// the bare connErr — so the error content alone proves whether it short-circuited.
	metaPath := config.ServerMetaPath(configDir, "svc")
	if err := os.Chmod(metaPath, 0400); err != nil {
		t.Fatalf("chmod marker read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(metaPath, 0600) }) //nolint:errcheck

	err := srv.markOAuthIfRequired(context.Background(), sc, connErr)
	if err == nil || !strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Errorf("second call: err = %v, want the oauth-required error (a rewrite attempt would have failed and returned the bare connErr instead)", err)
	}
}

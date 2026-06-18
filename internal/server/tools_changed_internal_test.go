//go:build test

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func newInternalToolsChangedTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	return srv
}

func toolDefs(names ...string) []transport.ToolDefinition {
	defs := make([]transport.ToolDefinition, len(names))
	for i, n := range names {
		defs[i] = transport.ToolDefinition{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`)}
	}
	return defs
}

func TestUpstreamRefresh_singleFlightCoalesces(t *testing.T) {
	u := &upstreamServer{}
	if !u.beginRefresh() {
		t.Fatal("first beginRefresh must win and run the loop")
	}
	if u.beginRefresh() || u.beginRefresh() {
		t.Fatal("concurrent beginRefresh calls must coalesce into the active run")
	}
	if !u.endRefresh() {
		t.Fatal("endRefresh must report the coalesced work as pending")
	}
	if u.endRefresh() {
		t.Fatal("endRefresh must report no work once pending is drained")
	}
	if !u.beginRefresh() {
		t.Fatal("a fresh cycle after completion must win again")
	}
	if u.endRefresh() {
		t.Fatal("a clean cycle leaves nothing pending")
	}
}

func TestRefreshUpstreamTools_reindexesAndFansOut(t *testing.T) {
	srv := newInternalToolsChangedTestServer(t)
	fake := &transport.FakeConnection{Tools: toolDefs("a"), Responses: map[string]json.RawMessage{}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck

	sess := srv.sessions.getOrCreate("s1")
	ch := sess.enableNotifications()
	t.Cleanup(func() { sess.disableNotifications(ch); close(ch) })

	fake.Tools = toolDefs("a", "b", "c")
	srv.refreshUpstreamTools("svc")

	if got := srv.ToolCount("svc"); got != 3 {
		t.Fatalf("registry should reflect the refreshed tool set, got %d tools", got)
	}
	select {
	case msg := <-ch:
		var n struct {
			Method string `json:"method"`
		}
		json.Unmarshal(msg, &n) //nolint:errcheck
		if n.Method != transport.NotificationToolsChanged {
			t.Errorf("fanned-out notification method = %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not fan out a tools/list_changed notification")
	}
}

func TestRefreshUpstreamTools_unknownServerIsNoOp(t *testing.T) {
	srv := newInternalToolsChangedTestServer(t)
	srv.refreshUpstreamTools("nope") // must not panic
}

func TestReplaceRegistryToolsLocked_skipsRemovedUpstream(t *testing.T) {
	srv := newInternalToolsChangedTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{Tools: toolDefs("a")}) //nolint:errcheck

	u := srv.upstreamByName("svc")
	u.shutdown() // cancels u.ctx, as remove_server does under serverOpMu

	srv.replaceRegistryToolsLocked(u, toolDefs("a", "b", "c"))

	if got := srv.ToolCount("svc"); got != 1 {
		t.Fatalf("a cancelled upstream must not be resurrected with new tools, got %d", got)
	}
}

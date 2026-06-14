//go:build test

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestReplaceRegistryToolsLocked_usesReloadedAliases(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	tools := []transport.ToolDefinition{{Name: "list_pull_requests", InputSchema: json.RawMessage(`{}`)}}
	proj := map[string]*config.ProjectionConfig{"list_pull_requests": {Alias: "old_alias"}}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh", Projections: proj}, &transport.FakeConnection{Tools: tools})

	srv.replaceProjections(map[string]map[string]*config.ProjectionConfig{
		"gh": {"list_pull_requests": {Alias: "new_alias"}},
	})

	upstreams := srv.snapshotUpstreams()
	if len(upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(upstreams))
	}
	srv.replaceRegistryToolsLocked(upstreams[0], tools)

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

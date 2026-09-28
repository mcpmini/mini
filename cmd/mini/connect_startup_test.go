//go:build test

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestBuildAndStart_ReturnsBeforeUpstreamResolves(t *testing.T) {
	_, url := hangingHTTPServer(t)
	dir := shortConfigDir(t)
	p := hungUpstreamBuildParams(t, dir, url)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	srv := buildAndStart(ctx, p)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("buildAndStart blocked for %v; want a near-immediate return", elapsed)
	}
	if got := srv.ToolCount("hung"); got != 0 {
		t.Fatalf("expected hung upstream to have 0 tools registered yet, got %d", got)
	}

	cancel()
	waitForClose(t, srv)
}

const cleanupTimerAndPollTicker = 2

func TestBuildAndStart_ProjectionHotReload(t *testing.T) {
	dir := shortConfigDir(t)
	writeServer(t, dir, "svc", "name: svc\ncommand: echo\n")
	writeServer(t, dir, "svc.proj", "getData:\n  include_only: [a, b]\n")
	reloaded := logSignal{msg: "projections reloaded", seen: make(chan struct{}, 1)}
	fc := clock.NewFake()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := buildAndStart(t.Context(), BuildServerParams{Cfg: cfg, ConfigDir: dir, Logger: slog.New(reloaded), Clock: fc})
	defer srv.Close()
	addGetDataUpstream(t, srv)
	waitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := fc.BlockUntilContext(waitCtx, cleanupTimerAndPollTicker); err != nil {
		t.Fatal("projection poller not started:", err)
	}

	if data := projectedGetData(t, srv); data["a"] == nil || data["b"] == nil {
		t.Fatalf("initial projection should keep a and b, got %v", data)
	}

	writeServer(t, dir, "svc.proj", "getData:\n  include_only: [a]\n")
	fc.Advance(5 * time.Second)
	reloaded.wait(t)

	if data := projectedGetData(t, srv); data["a"] == nil || data["b"] != nil {
		t.Fatalf("edited projection (include_only: [a]) not applied after hot reload, got %v", data)
	}
}

func addGetDataUpstream(t *testing.T, srv *server.Server) {
	t.Helper()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "getData", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`),
		},
	}
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}
}

type logSignal struct {
	msg  string
	seen chan struct{}
}

func (h logSignal) Enabled(context.Context, slog.Level) bool { return true }
func (h logSignal) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		select {
		case h.seen <- struct{}{}:
		default:
		}
	}
	return nil
}
func (h logSignal) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h logSignal) WithGroup(string) slog.Handler      { return h }

func (h logSignal) wait(t *testing.T) {
	t.Helper()
	select {
	case <-h.seen:
	case <-time.After(5 * time.Second):
		t.Fatalf("%q not logged within 5s", h.msg)
	}
}

func projectedGetData(t *testing.T, srv *server.Server) map[string]any {
	t.Helper()
	resp := serveSingleProxyCall(t, srv, "svc__getData")
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("getData call failed: %v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("getData returned no content: %v", result)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("getData returned non-JSON content %q: %v", text, err)
	}
	if data, ok := raw["data"].(map[string]any); ok {
		return data
	}
	return raw
}

func hungUpstreamBuildParams(t *testing.T, dir, url string) BuildServerParams {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	cfg.ResponseDir = filepath.Join(dir, "responses")
	sc := config.ServerConfig{Name: "hung", Transport: "http", URL: url}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return BuildServerParams{Cfg: cfg, ConfigDir: dir, Logger: logger, Servers: []config.ServerConfig{sc}}
}

func waitForClose(t *testing.T, srv closer) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after ctx cancel; connect goroutine leaked past the connect deadline")
	}
}

type closer interface{ Close() }

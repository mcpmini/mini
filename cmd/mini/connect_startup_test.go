package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func TestBuildAndStartConnecting_ReturnsBeforeUpstreamResolves(t *testing.T) {
	_, url := hangingHTTPServer(t)
	dir := shortConfigDir(t)
	p := hungUpstreamBuildParams(t, dir, url)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	srv := buildAndStartConnecting(ctx, p)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("buildAndStartConnecting blocked for %v; want a near-immediate return", elapsed)
	}
	if got := srv.ToolCount("hung"); got != 0 {
		t.Fatalf("expected hung upstream to have 0 tools registered yet, got %d", got)
	}

	cancel()
	waitForClose(t, srv)
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

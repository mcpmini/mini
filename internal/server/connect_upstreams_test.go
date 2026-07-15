//go:build test

package server_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

// notifications/tools/list_changed is the sole announcement channel — no new notification path (#33).
func TestConnectUpstreams_NotifiesLiveSessionOfLateUpstream(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, pr, &out) }()

	pw.Write(rpc("initialize", initParams(true)))            //nolint:errcheck
	pw.Write(notification("notifications/initialized", nil)) //nolint:errcheck

	srv.ConnectUpstreams(ctx, []config.ServerConfig{{Name: "dynamic", Transport: "http", URL: mcp.URL}})
	eventually(t, func() bool { return srv.ToolCount("dynamic") > 0 })

	pw.Close()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	msgs := parseMessages(out.Bytes())
	if !hasNotification(msgs, "notifications/tools/list_changed") {
		t.Errorf("expected notifications/tools/list_changed after late ConnectUpstreams; got: %v", msgs)
	}
}

func TestConnectUpstreams_CleanShutdownWithHungUpstream(t *testing.T) {
	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hung
	}))
	t.Cleanup(ts.Close)

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	srv.ConnectUpstreams(ctx, []config.ServerConfig{{Name: "hung", Transport: "http", URL: ts.URL}})
	cancel()

	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after ctx cancel; connect goroutine leaked")
	}
}

func TestConnectUpstreams_CloseWaitsForInFlightConnect(t *testing.T) {
	const connectDelay = 150 * time.Millisecond
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(connectDelay)
		fakeMCPHandle(w, r, []map[string]any{
			{"name": "slow_tool", "description": "slow", "inputSchema": map[string]any{"type": "object"}},
		})
	}))
	t.Cleanup(ts.Close)

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "slow", Transport: "http", URL: ts.URL}})

	start := time.Now()
	srv.Close()
	if elapsed := time.Since(start); elapsed < connectDelay {
		t.Fatalf("Close returned after %v, want >= %v (connectWg.Wait should block on the in-flight connect)", elapsed, connectDelay)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

var resolveHTTPAddrCases = []struct {
	in          string
	wantAddr    string
	wantNonLoop bool
}{
	{"4857", "127.0.0.1:4857", false},
	{":4857", "127.0.0.1:4857", false},
	{"127.0.0.1:4857", "127.0.0.1:4857", false},
	{"0.0.0.0:4857", "0.0.0.0:4857", true},
	{"192.168.1.1:4857", "192.168.1.1:4857", true},
	{"myhost:4857", "myhost:4857", true},
}

func TestResolveHTTPAddr(t *testing.T) {
	for _, tc := range resolveHTTPAddrCases {
		t.Run(tc.in, func(t *testing.T) {
			addr, nonLoop := resolveHTTPAddr(tc.in)
			if addr != tc.wantAddr {
				t.Errorf("addr: got %q, want %q", addr, tc.wantAddr)
			}
			if nonLoop != tc.wantNonLoop {
				t.Errorf("nonLoopback: got %v, want %v", nonLoop, tc.wantNonLoop)
			}
		})
	}
}

func TestParseToolMode(t *testing.T) {
	cases := []struct {
		in   string
		want transport.ToolMode
	}{
		{"", transport.ToolModeProxy},
		{"proxy", transport.ToolModeProxy},
		{"compact", transport.ToolModeCompact},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseToolMode(tc.in); got != tc.want {
				t.Errorf("parseToolMode(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

type fakeSessionEvictor struct {
	calls chan time.Duration
}

func (f *fakeSessionEvictor) RunSessionEviction(_ context.Context, maxIdle time.Duration) {
	f.calls <- maxIdle
}

func TestMaybeStartSessionEviction_skipsWithoutHTTPServer(t *testing.T) {
	fake := &fakeSessionEvictor{calls: make(chan time.Duration, 1)}
	maybeStartSessionEviction(context.Background(), nil, fake)
	select {
	case got := <-fake.calls:
		t.Fatalf("unexpected eviction start with nil HTTP server: %v", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestMaybeStartSessionEviction_startsWithHTTPServer(t *testing.T) {
	fake := &fakeSessionEvictor{calls: make(chan time.Duration, 1)}
	maybeStartSessionEviction(context.Background(), &http.Server{}, fake)
	select {
	case got := <-fake.calls:
		if got != standaloneHTTPSessionMaxIdle {
			t.Fatalf("maxIdle = %v, want %v", got, standaloneHTTPSessionMaxIdle)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session eviction to start")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outCh <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-outCh
}

func shortConfigDir(t *testing.T) string { return testutil.ShortTempDir(t) }

func socketHealthServer(t *testing.T, dir, body string) {
	t.Helper()
	sp := daemon.SocketPath(dir)
	os.MkdirAll(filepath.Dir(sp), 0700) //nolint:errcheck
	ln, err := net.Listen("unix", sp)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)                  //nolint:errcheck
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck
}

func TestRunDaemonStatusNotRunning(t *testing.T) {
	out := captureStdout(t, func() { runDaemonStatus(shortConfigDir(t)) })
	if out != "daemon: not running\n" {
		t.Fatalf("stdout = %q, want not running message", out)
	}
}

func TestRunDaemonStatusRunning(t *testing.T) {
	dir := shortConfigDir(t)
	socketHealthServer(t, dir, `{"ok":true}`)

	out := captureStdout(t, func() { runDaemonStatus(dir) })
	if !strings.Contains(out, "daemon: running") {
		t.Fatalf("expected running message, got %q", out)
	}
	if !strings.Contains(out, `{"ok":true}`) {
		t.Fatalf("expected health body in output, got %q", out)
	}
}

func TestRunDaemonStatusStaleSocket(t *testing.T) {
	dir := shortConfigDir(t)
	sp := daemon.SocketPath(dir)
	os.MkdirAll(filepath.Dir(sp), 0700) //nolint:errcheck
	if err := os.WriteFile(sp, nil, 0600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runDaemonStatus(dir) })
	if out != "daemon: not running\n" {
		t.Fatalf("stale socket should read as not running, got %q", out)
	}
}

func hangingHTTPServer(t *testing.T) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-hung
		}),
	}
	go srv.Serve(ln) //nolint:errcheck
	return srv, "http://" + ln.Addr().String() + "/"
}

func TestDaemonShutdown_boundedContextUnblocksWithHungHandler(t *testing.T) {
	srv, url := hangingHTTPServer(t)

	go http.Get(url) //nolint:errcheck,noctx
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown with bounded context blocked past deadline — hung handler prevented exit")
	}
}

func TestStartDaemonHTTP_shutdownClosesOpenNotificationStream(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	socketDir := shortConfigDir(t)
	ln := bindSocket(daemon.SocketPath(socketDir))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		startDaemonHTTP(ctx, DaemonHTTPParams{Srv: srv, Listener: ln})
	}()
	t.Cleanup(cancel)

	client := daemon.SocketClient(daemon.SocketPath(socketDir), 0)
	sessionID := initializeDaemonSession(t, client)
	stream := openDaemonStream(t, client, sessionID)
	defer stream.Body.Close()

	streamDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stream.Body)
		streamDone <- err
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startDaemonHTTP did not return promptly with an open notification stream")
	}
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("stream read error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("notification stream did not close after shutdown")
	}
}

func initializeDaemonSession(t *testing.T, client *http.Client) string {
	t.Helper()
	resp := postDaemonRPC(t, client, "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	defer resp.Body.Close()
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize response missing Mcp-Session-Id")
	}
	postDaemonNotification(t, client, sessionID, transport.NotificationInitialized)
	return sessionID
}

func postDaemonNotification(t *testing.T, client *http.Client, sessionID, method string) {
	t.Helper()
	resp := postDaemonRPC(t, client, sessionID, map[string]any{"jsonrpc": "2.0", "method": method})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification %s status = %d", method, resp.StatusCode)
	}
}

func postDaemonRPC(t *testing.T, client *http.Client, sessionID string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func openDaemonStream(t *testing.T, client *http.Client, sessionID string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://localhost/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("stream status = %d body=%s", resp.StatusCode, body)
	}
	return resp
}

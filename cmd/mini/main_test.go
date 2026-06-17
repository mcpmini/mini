package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
		{"", transport.ToolModePassthrough},
		{"passthrough", transport.ToolModePassthrough},
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

func healthServer(t *testing.T, body string) (port int, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck

	return ln.Addr().(*net.TCPAddr).Port, func() {
		_ = srv.Close()
		_ = ln.Close()
	}
}

func writeDaemonPortFile(t *testing.T, dir string, portText string) string {
	t.Helper()
	path := filepath.Join(dir, "daemon.port")
	if err := os.WriteFile(path, []byte(portText), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestRunDaemonStatusNotRunning(t *testing.T) {
	out := captureStdout(t, func() { runDaemonStatus(t.TempDir()) })
	if out != "daemon: not running\n" {
		t.Fatalf("stdout = %q, want not running message", out)
	}
}

func TestRunDaemonStatusInvalidPortFile(t *testing.T) {
	dir := t.TempDir()
	portFile := writeDaemonPortFile(t, dir, "not-a-port")

	out := captureStdout(t, func() { runDaemonStatus(dir) })
	want := "daemon: port file " + portFile + " contains invalid port\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestRunDaemonStatusRunning(t *testing.T) {
	dir := t.TempDir()
	port, closeFn := healthServer(t, `{"ok":true}`)
	defer closeFn()
	writeDaemonPortFile(t, dir, strconv.Itoa(port))

	out := captureStdout(t, func() { runDaemonStatus(dir) })
	if !strings.Contains(out, "daemon: running on port "+strconv.Itoa(port)) {
		t.Fatalf("expected running message, got %q", out)
	}
	if !strings.Contains(out, `{"ok":true}`) {
		t.Fatalf("expected health body in output, got %q", out)
	}
}

func TestRunDaemonStatusStalePortFile(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	writeDaemonPortFile(t, dir, strconv.Itoa(port))

	out := captureStdout(t, func() { runDaemonStatus(dir) })
	if !strings.Contains(out, "daemon: port file exists (port "+strconv.Itoa(port)+") but not responding") {
		t.Fatalf("expected stale port warning, got %q", out)
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

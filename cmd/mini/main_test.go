package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
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

// Short path: macOS caps Unix socket paths at 104 bytes, which t.TempDir() exceeds.
func shortConfigDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "mini")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) //nolint:errcheck
	return dir
}

func socketHealthServer(t *testing.T, dir, body string) {
	t.Helper()
	ln, err := net.Listen("unix", daemon.SocketPath(dir))
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
	if err := os.WriteFile(daemon.SocketPath(dir), nil, 0600); err != nil {
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

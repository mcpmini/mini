package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
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

func shortConfigDir(t *testing.T) string { return testutil.ShortTempDir(t) }

func socketHealthServer(t *testing.T, dir, body string) {
	t.Helper()
	sp := daemon.SocketPath(dir)
	if err := os.MkdirAll(filepath.Dir(sp), 0o700); err != nil {
		t.Fatalf("mkdir socket directory: %v", err)
	}
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
	var err error
	out := testutil.CaptureStdout(t, func() { err = runDaemonStatus(shortConfigDir(t)) })
	if err != nil {
		t.Fatalf("runDaemonStatus() error = %v", err)
	}
	if out != "daemon: not running\n" {
		t.Fatalf("stdout = %q, want not running message", out)
	}
}

func TestRunDaemonStatusRunning(t *testing.T) {
	dir := shortConfigDir(t)
	socketHealthServer(t, dir, `{"ok":true}`)

	var err error
	out := testutil.CaptureStdout(t, func() { err = runDaemonStatus(dir) })
	if err != nil {
		t.Fatalf("runDaemonStatus() error = %v", err)
	}
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
	testutil.WriteFile(t, sp, "")
	var err error
	out := testutil.CaptureStdout(t, func() { err = runDaemonStatus(dir) })
	if err != nil {
		t.Fatalf("runDaemonStatus() error = %v", err)
	}
	if out != "daemon: not running\n" {
		t.Fatalf("stale socket should read as not running, got %q", out)
	}
}

type partialReadErrorReader struct{ err error }

func (r partialReadErrorReader) Read(p []byte) (int, error) {
	return copy(p, "partial"), r.err
}

func TestConsumeDaemonStatusRejectsPartialBody(t *testing.T) {
	wantErr := errors.New("broken response body")
	var err error
	out := testutil.CaptureStdout(t, func() {
		err = consumeDaemonStatus(http.StatusOK, partialReadErrorReader{err: wantErr})
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("consumeDaemonStatus() error = %v, want wrapped read error", err)
	}
	if out != "" {
		t.Fatalf("partial health body was printed as status: %q", out)
	}
}

func TestConsumeDaemonStatusPreservesUnhealthyHTTPStatus(t *testing.T) {
	var err error
	out := testutil.CaptureStdout(t, func() {
		err = consumeDaemonStatus(http.StatusServiceUnavailable, strings.NewReader("unavailable"))
	})
	if err != nil {
		t.Fatalf("consumeDaemonStatus() error = %v", err)
	}
	if out != "daemon: unhealthy (HTTP 503) — unavailable\n" {
		t.Fatalf("unhealthy status output = %q", out)
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

func TestShutdownHTTPWithContextClosesActiveConnectionAfterShutdownFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
		close(finished)
	})}
	defer srv.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	defer func() {
		close(release)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("handler did not finish after release")
		}
	}()

	requestDone := make(chan error, 1)
	go func() {
		resp, requestErr := http.Get("http://" + ln.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
		requestDone <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request handler did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = shutdownHTTPWithContext(srv, ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdownHTTPWithContext() error = %v, want canceled shutdown", err)
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() error = %v, want server closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop after forced close")
	}
	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("active request succeeded after forced connection close")
		}
	case <-time.After(time.Second):
		t.Fatal("active request remained blocked after forced connection close")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
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

func countingTokenEndpoint(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"refreshed","token_type":"Bearer","expires_in":3600}`) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func fakeMCPUpstream(t *testing.T, gotAuth *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": transport.ProtocolVersion},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": []any{map[string]any{"name": "t1", "inputSchema": map[string]any{}}}},
			})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func oauthServerConfig(name, mcpURL, tokenURL string, enabled bool) config.ServerConfig {
	return config.ServerConfig{
		Name: name, Transport: "http", URL: mcpURL, Enabled: &enabled,
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: tokenURL},
	}
}

func TestServeStartup_zeroTokenEndpointCallsBeforeFirstRequest(t *testing.T) {
	configDir := t.TempDir()
	tokenSrv, tokenHits := countingTokenEndpoint(t)
	var gotAuth atomic.Value
	mcp := fakeMCPUpstream(t, &gotAuth)

	validTok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
	if err := auth.Save(configDir, "live", validTok); err != nil {
		t.Fatal(err)
	}
	expiredTok := &oauth2.Token{AccessToken: "dead-access", RefreshToken: "r2", Expiry: time.Now().Add(-time.Hour)}
	if err := auth.Save(configDir, "idle", expiredTok); err != nil {
		t.Fatal(err)
	}
	servers := []config.ServerConfig{
		oauthServerConfig("live", mcp.URL, tokenSrv.URL, true),
		oauthServerConfig("idle", "http://localhost:1", tokenSrv.URL, false),
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := buildAndStartConnecting(context.Background(),
		BuildServerParams{Cfg: &config.Config{}, ConfigDir: configDir, Logger: logger, Servers: servers},
		server.WithAuthProviders())
	defer srv.Close()

	deadline := time.Now().Add(5 * time.Second)
	for srv.ToolCount("live") != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.ToolCount("live") != 1 {
		t.Fatalf("expected live upstream to connect, tool count = %d", srv.ToolCount("live"))
	}
	if got := gotAuth.Load(); got != "Bearer stored-access" {
		t.Errorf("upstream Authorization = %v, want stored token via provider", got)
	}
	if tokenHits.Load() != 0 {
		t.Errorf("token endpoint hits at startup = %d, want 0", tokenHits.Load())
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

//go:build test

package server_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newConnectTestServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func mustCloseWithin(t *testing.T, srv *server.Server, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("Close did not return within deadline")
	}
}

func hungHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	hung := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hung
	}))
	// LIFO: close(hung) runs first so ts.Close doesn't wait on the blocked handler.
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(hung) })
	return ts
}

func TestConnectUpstreams_NotifiesLiveSessionOfLateUpstream(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newConnectTestServer(t)
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
	ts := hungHTTPServer(t)
	srv := newConnectTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	srv.ConnectUpstreams(ctx, []config.ServerConfig{{Name: "hung", Transport: "http", URL: ts.URL}})
	cancel()
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestConnectUpstreams_CloseCancelsInFlightConnect(t *testing.T) {
	const upstreamDelay = 5 * time.Second
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(upstreamDelay)
		fakeMCPHandle(w, r, []map[string]any{
			{"name": "slow_tool", "description": "slow", "inputSchema": map[string]any{"type": "object"}},
		})
	}))
	t.Cleanup(ts.Close)

	srv := newConnectTestServer(t)
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "slow", Transport: "http", URL: ts.URL}})
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestConnectUpstreams_CloseUnblocksHungConnectWithNoTimeout(t *testing.T) {
	ts := hungHTTPServer(t)
	srv := newConnectTestServer(t)

	sc := config.ServerConfig{Name: "hung", Transport: "http", URL: ts.URL, HandshakeTimeout: "0"}
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{sc})
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestConnectUpstreams_NormalConnectUnaffectedByFix(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newConnectTestServer(t)

	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: mcp.URL, HandshakeTimeout: "0"}
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{sc})
	eventually(t, func() bool { return srv.ToolCount("svc") > 0 })
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestConnectUpstreams_FastUpstreamRegistersWhileHungStillConnecting(t *testing.T) {
	fast := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	hung := hungHTTPServer(t)
	srv := newConnectTestServer(t)
	defer srv.Close()

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "fast", Transport: "http", URL: fast.URL},
		{Name: "hung", Transport: "http", URL: hung.URL},
	})
	eventually(t, func() bool { return srv.ToolCount("fast") > 0 })

	if srv.ToolCount("hung") != 0 {
		t.Fatal("hung upstream should not have tools yet")
	}
}

func TestConnectUpstreams_SkipsDisabledServer(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newConnectTestServer(t)
	defer srv.Close()

	enabled := false
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "off", Transport: "http", URL: mcp.URL, Enabled: &enabled},
	})
	// Give it a moment — if a goroutine was incorrectly launched, it would register tools.
	time.Sleep(200 * time.Millisecond)
	if srv.ToolCount("off") != 0 {
		t.Fatal("disabled server should not have any tools registered")
	}
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestConnectUpstreams_SecondCallCancelsPriorWorkers(t *testing.T) {
	hung := hungHTTPServer(t)
	fast := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newConnectTestServer(t)

	// First call: hung upstream that will never resolve
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "hung", Transport: "http", URL: hung.URL},
	})
	// Second call: overwrites cancelConnect, should cancel the first batch
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "fast", Transport: "http", URL: fast.URL},
	})
	eventually(t, func() bool { return srv.ToolCount("fast") > 0 })

	// Close should not hang — the first call's hung worker should have been canceled
	mustCloseWithin(t, srv, 3*time.Second)
}

func TestServerClose_inFlightOAuthRefresh_isAborted(t *testing.T) {
	reached := make(chan struct{}, 1)
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		select {
		case <-released:
		case <-r.Context().Done():
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(func() { release(); tokenSrv.Close() })

	dir := t.TempDir()
	saveToken(t, dir, "oauth-svc", &oauth2.Token{
		AccessToken:  "expired-access",
		RefreshToken: "old-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	})

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{
		Name:      "oauth-svc",
		Transport: "http",
		URL:       tokenSrv.URL,
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "test-client",
			TokenURL: tokenSrv.URL,
		},
	}})

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}

	mustCloseWithin(t, srv, 5*time.Second)
}

func newConnectTestServerWithClock(t *testing.T, fakeClock *clock.Fake) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	return server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithClock(fakeClock))
}

// responseCleanupTimer is the 1 timer the response store always registers on start.
const responseCleanupTimer = 1

func TestConnectUpstreamAsync_transientFailure_retriesAndRegisters(t *testing.T) {
	firstDone := make(chan struct{}, 1)
	var hits atomic.Int32
	tools := []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// 500 is not retried by the transport layer (only 429/503 are).
			http.Error(w, "internal error", http.StatusInternalServerError)
			select {
			case firstDone <- struct{}{}:
			default:
			}
			return
		}
		fakeMCPHandle(w, r, tools)
	}))
	t.Cleanup(ts.Close)

	fakeClock := clock.NewFake()
	srv := newConnectTestServerWithClock(t, fakeClock)
	defer srv.Close()

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "svc", Transport: "http", URL: ts.URL},
	})

	// Wait for the first attempt to fail before blocking on the backoff timer.
	// Without this, ConnectUpstreams returns immediately (goroutine not yet
	// scheduled) and BlockUntilContext races against timer registration.
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt not made within 5s")
	}

	// Wait for the backoff sleep timer (plus the response store cleanup timer).
	if err := fakeClock.BlockUntilContext(t.Context(), responseCleanupTimer+1); err != nil {
		t.Fatalf("waiting for retry backoff timer: %v", err)
	}
	fakeClock.Advance(time.Second)

	eventually(t, func() bool { return srv.ToolCount("svc") > 0 })
}

func TestConnectUpstreamAsync_reauthFailure_stopsRetrying(t *testing.T) {
	reached := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	fakeClock := clock.NewFake()
	srv := newConnectTestServerWithClock(t, fakeClock)
	defer srv.Close()

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "svc", Transport: "http", URL: ts.URL},
	})

	// Wait for the initial dial attempt to land.
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("initial dial not reached within 5s")
	}

	// Give the goroutine time to process the error. It returns without sleeping
	// (ErrReauthRequired stops the loop), so no backoff timer should be created.
	time.Sleep(100 * time.Millisecond)

	// Assert no backoff timer was registered. If one were, this would return nil.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, responseCleanupTimer+1); err == nil {
		t.Error("backoff timer should not be registered for reauth error")
	}
}

func TestConnectUpstreamAsync_closeDuringRetry_returnsPromptly(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)

	fakeClock := clock.NewFake()
	srv := newConnectTestServerWithClock(t, fakeClock)

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{
		{Name: "svc", Transport: "http", URL: ts.URL},
	})

	// Wait for the retry backoff timer to be registered.
	if err := fakeClock.BlockUntilContext(t.Context(), responseCleanupTimer+1); err != nil {
		t.Fatalf("waiting for retry backoff timer: %v", err)
	}

	// Close cancels the connect context, which must unblock sleepBackoffCtx.
	mustCloseWithin(t, srv, 3*time.Second)
}

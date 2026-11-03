//go:build test

package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func newConnectTestServer(t *testing.T) *server.Server {
	t.Helper()
	return newConnectTestServerLogging(t, slog.NewTextHandler(io.Discard, nil), nil)
}

func newConnectTestServerLogging(t *testing.T, logs slog.Handler, clk clock.Clock) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	return newTestServer(t, server.Params{Config: cfg, Logger: slog.New(logs), Clock: clk})
}

func mustClose(t *testing.T, srv *server.Server) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	testutil.WaitForReceive(t, done, "Close to return")
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
	mcp := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
	srv := newConnectTestServer(t)
	agent := openAgent(t, srv)

	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "dynamic", Transport: "http", URL: mcp.URL}})
	agent.waitForLine("notifications/tools/list_changed")
}

func TestConnectUpstreams_CleanShutdownWithHungUpstream(t *testing.T) {
	ts := hungHTTPServer(t)
	srv := newConnectTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	srv.ConnectUpstreams(ctx, []config.ServerConfig{{Name: "hung", Transport: "http", URL: ts.URL}})
	cancel()
	mustClose(t, srv)
}

func TestConnectUpstreams_CloseCancelsInFlightConnect(t *testing.T) {
	const upstreamDelay = 5 * time.Second
	ts := mcptest.NewHTTP(t, mcptest.Upstream{
		Tools: []mcptest.Tool{{Name: "slow_tool", Description: "slow"}},
		Intercept: func(http.ResponseWriter, *http.Request) bool {
			time.Sleep(upstreamDelay)
			return false
		},
	})

	srv := newConnectTestServer(t)
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "slow", Transport: "http", URL: ts.URL}})
	mustClose(t, srv)
}

func TestConnectUpstreams_CloseUnblocksHungConnectWithNoTimeout(t *testing.T) {
	ts := hungHTTPServer(t)
	srv := newConnectTestServer(t)

	sc := config.ServerConfig{Name: "hung", Transport: "http", URL: ts.URL, HandshakeTimeout: "0"}
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{sc})
	mustClose(t, srv)
}

func TestConnectUpstreams_NormalConnectUnaffectedByFix(t *testing.T) {
	mcp := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
	srv := newConnectTestServer(t)

	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: mcp.URL, HandshakeTimeout: "0"}
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{sc})
	eventually(t, func() bool { return srv.ToolCount("svc") > 0 })
	mustClose(t, srv)
}

func TestConnectUpstreams_FastUpstreamRegistersWhileHungStillConnecting(t *testing.T) {
	fast := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
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
	mcp := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
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
	mustClose(t, srv)
}

func TestConnectUpstreams_closeCancelsConnectsFromEveryCall(t *testing.T) {
	hung := hungHTTPServer(t)
	srv := newConnectTestServer(t)
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "first", Transport: "http", URL: hung.URL}})
	srv.ConnectUpstreams(
		context.Background(),
		[]config.ServerConfig{{Name: "second", Transport: "http", URL: hung.URL}},
	)

	mustClose(t, srv)
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
	sc := config.ServerConfig{
		Name:      "oauth-svc",
		Transport: "http",
		URL:       tokenSrv.URL,
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "test-client",
			TokenURL: tokenSrv.URL,
		},
	}
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "oauth-svc",
		Server:     sc,
		Token: &oauth2.Token{
			AccessToken:  "expired-access",
			RefreshToken: "old-refresh",
			Expiry:       time.Now().Add(-time.Hour),
		},
	})

	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	srv := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{sc})

	testutil.WaitForReceive(t, reached, "the token endpoint to be reached")

	mustClose(t, srv)
}

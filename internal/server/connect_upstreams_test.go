//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

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

func TestConnectUpstreams_transientOAuthRefreshRecovers(t *testing.T) {
	var tokenHits atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tokenHits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "valid-token", "refresh_token": "r2",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"tools": []map[string]any{
					{"name": "do_thing", "description": "d", "inputSchema": map[string]any{"type": "object"}},
				}},
			})
		}
	}))
	t.Cleanup(mcpSrv.Close)

	dir := t.TempDir()
	saveToken(t, dir, "oauth-svc", &oauth2.Token{
		AccessToken:  "old-token",
		RefreshToken: "stored-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	writeServerYAML(t, dir, "oauth-svc", "name: oauth-svc\ntransport: http\nurl: "+mcpSrv.URL+
		"\nauth:\n  type: oauth2\n  client_id: cid\n  token_endpoint_auth_method: client_secret_post\n  token_url: "+tokenSrv.URL+"\n")

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowPrivateURLs = true
	srv := server.NewWithConfigDir(cfg, dir,
		slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithAuthProviders())
	defer srv.Close()

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{
		Name: "oauth-svc", Transport: "http", URL: mcpSrv.URL,
		Auth: &config.AuthConfig{
			Type:                    config.AuthTypeOAuth2,
			ClientID:                "cid",
			TokenEndpointAuthMethod: "client_secret_post",
			TokenURL:                tokenSrv.URL,
		},
	}})
	eventually(t, func() bool { return srv.ToolCount("oauth-svc") > 0 })
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

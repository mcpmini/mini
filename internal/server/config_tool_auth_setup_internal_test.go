//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestConfigureStartAuth_canceledRequestSkipsDiscovery(t *testing.T) {
	var requests atomic.Int32
	upstream := testutil.Serve(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name: "svc", URL: upstream.URL,
		Auth: authConfigWithFreeCallbackPort(t, &config.AuthConfig{Type: config.AuthTypeOAuth2}),
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := configureStartAuth(ctx, srv)

	if !errors.Is(err, context.Canceled) || requests.Load() != 0 || authFlow(srv, "svc") != nil {
		t.Fatalf("start_auth error=%v discovery requests=%d flow=%v", err, requests.Load(), authFlow(srv, "svc"))
	}
}

func TestConfigureStartAuth_canceledDiscoveryLeavesNoFlow(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	cancel, started, result := startStalledAuthSetup(t, srv)
	testutil.WaitForReceive(t, started, "the discovery request")

	cancel()

	err := testutil.WaitForReceive(t, result, "start_auth to return without waiting for the stalled handler")
	if !errors.Is(err, context.Canceled) || authFlow(srv, "svc") != nil {
		t.Fatalf("start_auth error=%v flow=%v", err, authFlow(srv, "svc"))
	}
}

func TestConfigureStartAuth_handoffSurvivesRequestCancellationAndCloseJoins(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	srv.cfg.DisableAuthBrowserOpen = true
	tokenServer := authtest.NewTokenServer(t)
	upstreamURL, initializing := stalledAuthInitialize(t, tokenServer.Srv.URL)
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name: "svc", Transport: "http", URL: upstreamURL,
		Auth: authConfigWithFreeCallbackPort(t, tokenServer.AuthConfig()),
	})
	ctx, cancel := context.WithCancel(t.Context())
	_, err := configureStartAuth(ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	flow := authFlow(srv, "svc")
	if flow == nil {
		t.Fatal("start_auth did not register the OAuth flow")
	}
	auth.BufferCallbackCode(flow.login, "test-auth-code")
	testutil.WaitForReceive(t, initializing, "the OAuth reconnect to start after the configure request was canceled")
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	testutil.WaitForReceive(t, closed, "Close to cancel and join the handed-off OAuth flow")
	if authFlow(srv, "svc") != nil {
		t.Fatal("OAuth flow remained registered after Close")
	}
}

func startStalledAuthSetup(t *testing.T, srv *Server) (context.CancelFunc, <-chan struct{}, <-chan error) {
	t.Helper()
	started, release := make(chan struct{}, 1), make(chan struct{})
	upstream := testutil.Serve(t, func(http.ResponseWriter, *http.Request) {
		started <- struct{}{}
		<-release
	})
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name: "svc", URL: upstream.URL,
		Auth: authConfigWithFreeCallbackPort(t, &config.AuthConfig{Type: config.AuthTypeOAuth2}),
	})
	ctx, cancel := context.WithCancel(t.Context())
	result, done := make(chan error, 1), make(chan struct{})
	go func() { _, err := configureStartAuth(ctx, srv); result <- err; close(done) }()
	t.Cleanup(func() {
		close(release)
		testutil.WaitForReceive(t, done, "start_auth to finish after discovery is released")
	})
	t.Cleanup(cancel)
	return cancel, started, result
}

func configureStartAuth(ctx context.Context, srv *Server) (any, error) {
	return srv.handleConfigure(ctx, json.RawMessage(`{"action":"start_auth","server":"svc"}`), nil)
}

func authFlow(srv *Server, name string) *authFlowState {
	srv.authMu.Lock()
	defer srv.authMu.Unlock()
	return srv.authFlows[name]
}

func authConfigWithFreeCallbackPort(t *testing.T, ac *config.AuthConfig) *config.AuthConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ac.CallbackPort = listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return ac
}

//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestConfigureStartAuth_canceledRequestSkipsDiscovery(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(upstream.Close)
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
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("discovery request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || authFlow(srv, "svc") != nil {
			t.Fatalf("start_auth error=%v flow=%v", err, authFlow(srv, "svc"))
		}
	case <-time.After(time.Second):
		t.Fatal("start_auth waited for the stalled metadata handler after cancellation")
	}
}

func TestConfigureStartAuth_handoffSurvivesRequestCancellationAndCloseJoins(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	srv.cfg.DisableAuthBrowserOpen = true
	tokenServer := authtest.NewTokenServer(t)
	upstreamURL, initializing := stalledAuthInitialize(t)
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
	select {
	case <-initializing:
	case <-time.After(time.Second):
		t.Fatal("OAuth reconnect did not start after the configure request was canceled")
	}
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel and join the handed-off OAuth flow")
	}
	if authFlow(srv, "svc") != nil {
		t.Fatal("OAuth flow remained registered after Close")
	}
}

func startStalledAuthSetup(t *testing.T, srv *Server) (context.CancelFunc, <-chan struct{}, <-chan error) {
	t.Helper()
	started, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		started <- struct{}{}
		<-release
	}))
	t.Cleanup(upstream.Close)
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name: "svc", URL: upstream.URL,
		Auth: authConfigWithFreeCallbackPort(t, &config.AuthConfig{Type: config.AuthTypeOAuth2}),
	})
	ctx, cancel := context.WithCancel(t.Context())
	result, done := make(chan error, 1), make(chan struct{})
	go func() { _, err := configureStartAuth(ctx, srv); result <- err; close(done) }()
	t.Cleanup(func() {
		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("start_auth did not finish after discovery release")
		}
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

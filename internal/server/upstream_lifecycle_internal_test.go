//go:build test

package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestConnectUpstreams_afterClose_startsNothing(t *testing.T) {
	srv, _ := newRetryTestServer(t)
	srv.Close()

	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{unavailableServer(t)})

	if srv.upstreams.get("svc") != nil {
		t.Error("a startup connect after Close started svc, which nothing would stop")
	}
}

func TestAddConnection_afterClose_refusesAndClosesTheConn(t *testing.T) {
	srv, _ := newRetryTestServer(t)
	srv.Close()
	conn := &transport.FakeConnection{}

	err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn)

	if !errors.Is(err, errClosed) {
		t.Errorf("AddConnection after Close = %v, want errClosed", err)
	}
	if !conn.Closed {
		t.Error("the refused connection was left open")
	}
}

func TestStop_aConnectionWhoseCloseWaitsOnATokenRefresh_endsPromptly(t *testing.T) {
	for name, stop := range map[string]func(srv *Server){
		"remove_server": func(srv *Server) { _, _ = srv.removeServerFromAgent("svc") },
		"Close":         func(srv *Server) { srv.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			srv := newInstallTestServer(t)
			conn := &refreshingOnClose{FakeConnection: &transport.FakeConnection{}}
			if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
				t.Fatal(err)
			}
			conn.login, _, _ = expiredLoginProvider(t, srv)

			stopped := make(chan struct{})
			go func() { defer close(stopped); stop(srv) }()

			testutil.WaitForReceive(t, stopped, name+" to end svc while closing it waits on a token refresh")
		})
	}
}

// refreshingOnClose stands in for an HTTP connection whose Close waits for a notification listener
// that is refreshing the token.
type refreshingOnClose struct {
	*transport.FakeConnection
	login transport.AuthorizationProvider
}

func (c *refreshingOnClose) Close() error {
	// Only the wait matters; the refresh fails once the provider ends.
	_, _ = c.login.Authorization(context.Background())
	return c.FakeConnection.Close()
}

func newRetryTestServer(t *testing.T) (*Server, *clock.Fake) {
	t.Helper()
	srv := newInternalTestServer(t)
	srv.cfg.DangerousAllowPrivateURLs = true
	fc := clock.NewFake()
	srv.clock = fc
	return srv, fc
}

func unavailableServer(t *testing.T) config.ServerConfig {
	t.Helper()
	down := testutil.Serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	return config.ServerConfig{Name: "svc", Transport: "http", URL: down.URL}
}

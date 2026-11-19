//go:build test

package server

import (
	"errors"
	"testing"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestReplace_duringBackoff_endsTheOldRetryBeforeReturning(t *testing.T) {
	srv, fc := newRetryTestServer(t)
	old := startRetrying(t, srv, fc)
	unlock := srv.serverNames.lock("svc")
	defer unlock()

	srv.replace("svc", srv.newConfigServer(unavailableServer(t)))

	if old.isOpening() {
		t.Error("replace returned while the old svc still retries its connect")
	}
}

func TestReplace_underAFreeName_leavesALoginStartedForItRunning(t *testing.T) {
	srv, _ := newRetryTestServer(t)
	cancelled := false
	login := authtest.StartLogin(t, &config.AuthConfig{})
	srv.storeAuthFlow("svc", newAuthFlow(func() { cancelled = true }, login, ""))
	genAtStartAuth := srv.snapshotRemoveGen("svc")
	unlock := srv.serverNames.lock("svc")
	defer unlock()

	srv.replace("svc", srv.newConfigServer(unavailableServer(t)))

	if cancelled || srv.snapshotRemoveGen("svc") != genAtStartAuth {
		t.Error("svc starting under its free name ended the login started for it, which could no longer save its token")
	}
}

func TestReplaceConnected_afterClose_refusesAndClosesTheConn(t *testing.T) {
	srv, _ := newRetryTestServer(t)
	srv.Close()
	conn := &transport.FakeConnection{}

	err := srv.replaceConnected(srv.newConfigServer(config.ServerConfig{Name: "svc"}), connected{conn: conn})

	if !errors.Is(err, errClosed) {
		t.Errorf("replaceConnected after Close = %v, want errClosed", err)
	}
	if !conn.Closed {
		t.Error("the refused connection was left open")
	}
}

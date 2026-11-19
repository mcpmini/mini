//go:build test

package server_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolsChangedRefreshesOnlyNotifyingUpstream(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	one, two := fakeConn("old"), fakeConn("still")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "one", Connection: one})
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "two", Connection: two})

	one.SetTools(fakeConn("new").Tools)
	two.SetTools(fakeConn("wrong").Tools)
	emitToolsChanged(one)

	eventually(t, func() bool {
		tools := toolsList(t, srv)
		return containsName(tools, "one__new") &&
			!containsName(tools, "one__old") &&
			containsName(tools, "two__still") &&
			!containsName(tools, "two__wrong")
	})
}

func TestToolsChangedRefreshFailureKeepsOldCatalog(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("old")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	initialCalls := conn.ListToolsCallCount()

	conn.SetTools(fakeConn("new").Tools)
	conn.SetError(errors.New("list failed"))
	emitToolsChanged(conn)

	eventually(t, func() bool { return conn.ListToolsCallCount() > initialCalls })
	tools := toolsList(t, srv)
	if !containsName(tools, "svc__old") || containsName(tools, "svc__new") {
		t.Fatalf("failed refresh changed catalog: %v", toolNames(tools))
	}
}

func TestRemoveServerWhoseCloseDeliversToolsChanged(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	writeTestServerConfig(t, srv, config.ServerConfig{Name: "svc"})
	conn := notifyOnCloseConn{fakeConn("tool")}
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	removed := make(chan struct{})
	go func() {
		serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
		close(removed)
	}()

	testutil.WaitForReceive(t, removed, "remove_server to close a connection that sent tools/list_changed during Close")
	if srv.ToolCount("svc") != 0 {
		t.Error("svc still registered after remove_server")
	}
}

func TestToolsChangedRefreshFinishingAfterItsServerWasRemovedOrReplaced_publishesNothing(t *testing.T) {
	for _, tc := range svcLifecycleChanges {
		t.Run(tc.name, func(t *testing.T) {
			srv, finishRefresh := refreshHeldInListTools(t)

			tc.change(t, srv)
			finishRefresh()
			srv.Close() // waits for the refresh to finish

			assertSvcServes(t, srv, tc.servesAfter...)
		})
	}
}

func refreshHeldInListTools(t *testing.T) (srv *server.Server, finishRefresh func()) {
	t.Helper()
	srv, refresh := newTestServer(t, server.Params{}), newHeldStep(t)
	conn := &heldRefreshConn{FakeConnection: fakeConn("tool"), refresh: refresh}
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	conn.SetTools(fakeConn("late").Tools)
	emitToolsChanged(conn.FakeConnection)
	refresh.awaitReached("the refresh to list tools")
	return srv, refresh.release
}

type heldRefreshConn struct {
	*transport.FakeConnection
	refresh *heldStep
}

func (c *heldRefreshConn) ListTools(ctx context.Context) (transport.ToolList, error) {
	if c.ListToolsCallCount() > 0 {
		c.refresh.hold() // ignores ctx: the refresh must finish after the test's change
	}
	return c.FakeConnection.ListTools(ctx)
}

type notifyOnCloseConn struct{ *transport.FakeConnection }

func (c notifyOnCloseConn) Close() error {
	emitToolsChanged(c.FakeConnection)
	return c.FakeConnection.Close()
}

func emitToolsChanged(conn *transport.FakeConnection) {
	conn.EmitNotification(transport.Notification{
		JsonRPC: transport.JsonRPCVersion,
		Method:  transport.NotificationToolsChanged,
	})
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestToolsChangedRefresh_timingOutOnTheConnection_reconnects(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := &timingOutRefreshConn{FakeConnection: fakeConn("tool")}
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}

	emitToolsChanged(conn.FakeConnection)

	eventually(t, func() bool { return srv.IsReconnecting("svc") })
}

type timingOutRefreshConn struct{ *transport.FakeConnection }

func (c *timingOutRefreshConn) ListTools(ctx context.Context) (transport.ToolList, error) {
	if c.ListToolsCallCount() > 0 {
		return transport.ToolList{}, &transport.ConnectionError{
			Err: fmt.Errorf("tools/list: %w", context.DeadlineExceeded),
		}
	}
	return c.FakeConnection.ListTools(ctx)
}

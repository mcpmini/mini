//go:build test

package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolsChangedRefreshesOnlyNotifyingUpstream(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	one, two := fakeConn("old"), fakeConn("still")
	addProxyConn(t, srv, "one", one)
	addProxyConn(t, srv, "two", two)

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
	addProxyConn(t, srv, "svc", conn)
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

	select {
	case <-removed:
	case <-time.After(3 * time.Second):
		t.Fatal("remove_server hung closing a connection that delivered tools/list_changed during Close")
	}
	if srv.ToolCount("svc") != 0 {
		t.Error("svc still registered after remove_server")
	}
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

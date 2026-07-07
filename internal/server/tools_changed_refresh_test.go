//go:build test

package server_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func TestToolsChangedRefreshesOnlyNotifyingUpstream(t *testing.T) {
	srv := newProxyServer(t)
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
	srv := newProxyServer(t)
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

func emitToolsChanged(conn *transport.FakeConnection) {
	conn.EmitNotification(transport.Notification{
		JSONRPC: "2.0",
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

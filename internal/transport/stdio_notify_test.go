package transport

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestHandleNotification_invokesOnNotifyWithMethod(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	got := make(chan string, 1)
	conn.onNotify = func(method string) { got <- method }

	notif, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": NotificationToolsChanged})
	fmt.Fprintf(serverW, "%s\n", notif)

	select {
	case m := <-got:
		if m != NotificationToolsChanged {
			t.Errorf("got method %q, want %q", m, NotificationToolsChanged)
		}
	case <-time.After(time.Second):
		t.Fatal("onNotify was not invoked for a notification line")
	}
}

func TestHandleNotification_notInvokedForResponses(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	called := make(chan string, 1)
	conn.onNotify = func(method string) { called <- method }

	ch := conn.pending.register(int64(7))
	sendResponse(serverW, 7, "pong")
	<-ch

	select {
	case m := <-called:
		t.Errorf("onNotify must not fire for a response with an id, got %q", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleNotification_ignoresMethodlessNotification(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	called := make(chan struct{}, 1)
	conn.onNotify = func(string) { called <- struct{}{} }

	fmt.Fprintf(serverW, "%s\n", `{"jsonrpc":"2.0"}`)

	select {
	case <-called:
		t.Error("onNotify must not fire when the notification has no method")
	case <-time.After(100 * time.Millisecond):
	}
}

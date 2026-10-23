//go:build test

package transport

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestHandleMessage_deliversToWaiter(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	ch := conn.pending.register(int64(1))
	sendResponse(serverW, 1, map[string]any{"ok": true})

	select {
	case resp := <-ch:
		if resp.Error != nil {
			t.Errorf("unexpected error: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatch")
	}
}

func TestHandleMessage_ignoresNotification(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)

	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": JsonRPCVersion, "method": "notifications/initialized",
	})
	fmt.Fprintf(serverW, "%s\n", notif)

	// A subsequent response to a real pending call should still arrive.
	ch := conn.pending.register(int64(99))
	sendResponse(serverW, 99, "pong")

	select {
	case resp := <-ch:
		if resp.Error != nil {
			t.Errorf("unexpected error: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out — notification may have corrupted dispatch")
	}
}

func TestHandleMessage_buffersNotificationUntilHandlerInstalled(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	for range 32 {
		fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	}
	drain := conn.pending.register(int64(99))
	sendResponse(serverW, 99, map[string]any{"ok": true})
	select {
	case <-drain:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-handler notifications to drain")
	}

	received := make(chan Notification, 8)
	conn.SetNotificationHandler(func(notification Notification) { received <- notification })
	select {
	case notification := <-received:
		if notification.Method != NotificationToolsChanged {
			t.Fatalf("method = %q", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification emitted before handler installation was lost")
	}
	select {
	case notification := <-received:
		t.Fatalf("expected one coalesced pending invalidation, got %q", notification.Method)
	default:
	}

	fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	got := []string{
		mustReceiveNotification(t, received).Method,
		mustReceiveNotification(t, received).Method,
	}
	if !slices.Equal(got, []string{NotificationInitialized, NotificationToolsChanged}) {
		t.Fatalf("post-install notifications = %v", got)
	}
}

func mustReceiveNotification(t *testing.T, ch <-chan Notification) Notification {
	t.Helper()
	select {
	case notification := <-ch:
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
		return Notification{}
	}
}

func TestHandleMessage_rpcError_deliveredToWaiter(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	ch := conn.pending.register(int64(5))

	line, _ := json.Marshal(map[string]any{
		"jsonrpc": JsonRPCVersion, "id": 5,
		"error": map[string]any{"code": -32601, "message": "method not found"},
	})
	fmt.Fprintf(serverW, "%s\n", line)

	select {
	case resp := <-ch:
		if resp.Error == nil || resp.Error.Code != -32601 {
			t.Errorf("expected -32601 error, got: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

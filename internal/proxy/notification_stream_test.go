//go:build test

package proxy

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestNotificationStream_modernTaggingAndLegacyForwarding(t *testing.T) {
	session, out := newTestProxySession(t, transport.ToolModeProxy)
	notification := toolsChangedLine(t)
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(notification)+"\n" {
		t.Fatalf("legacy notification output = %q", out.String())
	}
	out.Reset()
	session.subs.modern.Store(true)
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("modern notification without listen was forwarded: %q", out.String())
	}
	listen := transport.NewSubscriptionAck(json.RawMessage(`9`), transport.ListenNotifications{ToolsListChanged: true})
	if err := session.subs.acknowledge(listen, func(transport.SubscriptionAck) error { return nil }); err != nil {
		t.Fatal(err)
	}
	session.restoreLifecycle(linkState{generation: 1})
	if got := proxyListenCount(session); got != 1 {
		t.Fatalf("listens after recovery = %d, want 1", got)
	}
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	tagged := decodeLine[transport.ToolsListChangedNotification](t, out.Bytes())
	if tagged.Method != transport.NotificationToolsChanged || string(tagged.Params.Meta.SubscriptionID) != "9" {
		t.Fatalf("tagged notification = %s", out.Bytes())
	}
	out.Reset()
	other := json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
	if err := session.notificationStream.forward(other); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("modern agent received a notification type it did not subscribe to: %q", out.String())
	}
}

func toolsChangedLine(t *testing.T) json.RawMessage {
	t.Helper()
	line, err := json.Marshal(
		transport.Notification{JsonRPC: transport.JsonRPCVersion, Method: transport.NotificationToolsChanged},
	)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

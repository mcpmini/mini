//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

func listenLine(id any) []byte {
	return transporttest.ModernRequest(id, transport.MethodSubscriptionsListen,
		transport.ListenParams{Notifications: transport.ListenNotifications{ToolsListChanged: true}})
}

func cancelLine(t *testing.T, id int) []byte {
	t.Helper()
	params, err := json.Marshal(transport.CancelledParams{RequestID: json.RawMessage(strconv.Itoa(id))})
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(transport.Notification{
		JsonRPC: transport.JsonRPCVersion, Method: transport.NotificationCancelled, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func TestProxySession_modernListenAcknowledgesAndCancelsLocally(t *testing.T) {
	session, out := newReadyProxySession(t, transport.ToolModeProxy)
	handled, err := session.handleModernInput(listenLine(5))
	if err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	ack := decodeLine[transport.SubscriptionAck](t, out.Bytes())
	if ack.Method != transport.NotificationSubscriptionsAcknowledged ||
		string(ack.Params.Meta.SubscriptionID) != "5" || !ack.Params.Notifications.ToolsListChanged {
		t.Fatalf("listen acknowledgment = %s", out.Bytes())
	}
	if got := proxyListenCount(session); got != 1 {
		t.Fatalf("registered listens = %d, want 1", got)
	}
	handled, err = session.handleModernInput(cancelLine(t, 5))
	if err != nil || !handled || proxyListenCount(session) != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf(
			"cancel handled=%t error=%v listens=%d output=%q",
			handled, err, proxyListenCount(session), out.String(),
		)
	}
	if err = session.notificationStream.forward(toolsChangedLine(t)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("notification arrived after cancel: %q", out.String())
	}
	handled, err = session.handleModernInput(cancelLine(t, 6))
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("unknown cancellation was consumed")
	}
}

func TestProxySession_modernListenCompactAcknowledgesEmptyFilter(t *testing.T) {
	session, out := newReadyProxySession(t, transport.ToolModeCompact)
	if handled, err := session.handleModernInput(listenLine("listen")); err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	if ack := decodeLine[transport.SubscriptionAck](t, out.Bytes()); ack.Params.Notifications.ToolsListChanged {
		t.Fatalf("compact acknowledgment = %s, want an empty filter", out.Bytes())
	}
	if got := proxyListenCount(session); got != 0 {
		t.Fatalf("compact registered listens = %d, want 0", got)
	}
}

func TestProxySession_modernListenWithMalformedParamsIsRejectedLocally(t *testing.T) {
	session, out := newReadyProxySession(t, transport.ToolModeProxy)
	listen := transporttest.ModernRequest(
		7,
		transport.MethodSubscriptionsListen,
		map[string]any{"notifications": "all"},
	)
	if handled, err := session.handleModernInput(listen); err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	response := decodeLine[transport.Response](t, out.Bytes())
	if response.Error == nil || response.Error.Code != transport.CodeInvalidParams || proxyListenCount(session) != 0 {
		t.Fatalf("malformed listen output = %s, listens = %d", out.Bytes(), proxyListenCount(session))
	}
}

func TestProxySession_modernListenWithWrongJSONRPCGoesToDaemon(t *testing.T) {
	session, out := newTestProxySession(t, transport.ToolModeProxy)
	listen := bytes.Replace(listenLine(5), []byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1)
	handled, err := handleLocal(t, session, listen)
	if err != nil || handled || out.Len() != 0 || proxyListenCount(session) != 0 {
		t.Fatalf("handled=%t error=%v output=%q listens=%d", handled, err, out.String(), proxyListenCount(session))
	}
}

func handleLocal(t *testing.T, session *proxySession, line []byte) (bool, error) {
	t.Helper()
	return session.handleLocalModernLine(decodeLine[modernLine](t, line))
}

func proxyListenCount(session *proxySession) int {
	count := 0
	_ = session.subs.forEach(func(json.RawMessage) error { //nolint:errcheck // the callback never fails.
		count++
		return nil
	})
	return count
}

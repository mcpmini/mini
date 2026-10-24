//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
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
	session, out := newReadyProxySession(t)
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

func TestProxySession_modernListenWithoutToolChangesAcknowledgesAnEmptyFilter(t *testing.T) {
	session, out := newReadyProxySession(t)
	listen := transporttest.ModernRequest(5, transport.MethodSubscriptionsListen, transport.ListenParams{})
	if handled, err := session.handleModernInput(listen); err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	if ack := decodeLine[transport.SubscriptionAck](t, out.Bytes()); ack.Params.Notifications.ToolsListChanged {
		t.Fatalf("acknowledgment = %s, want an empty filter", out.Bytes())
	}
	if got := proxyListenCount(session); got != 0 {
		t.Fatalf("registered listens = %d, want 0", got)
	}
	if err := session.notificationStream.forward(toolsChangedLine(t)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("tool change forwarded to a listen that didn't ask for it: %q", out.String())
	}
}

func TestProxySession_modernListenWithMalformedParamsIsRejectedLocally(t *testing.T) {
	session, out := newReadyProxySession(t)
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
	session, out := newTestProxySession(t)
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

func TestSubscriptions_acknowledgeRegistersOnlyAfterTheAckIsWritten(t *testing.T) {
	subs := newSubscriptions()
	want := errors.New("client gone")
	ack := transport.NewSubscriptionAck(json.RawMessage(`1`), transport.ListenNotifications{ToolsListChanged: true})
	if err := subs.acknowledge(ack, func(transport.SubscriptionAck) error { return want }); !errors.Is(err, want) {
		t.Fatalf("acknowledge error = %v, want the write failure", err)
	}
	if subs.remove(json.RawMessage(`1`)) {
		t.Fatal("listen registered although its acknowledgement was never written")
	}
}

func TestSubscriptions_forEachStopsAtTheFirstError(t *testing.T) {
	subs := newSubscriptions()
	for _, id := range []string{`1`, `2`} {
		ack := transport.NewSubscriptionAck(json.RawMessage(id), transport.ListenNotifications{ToolsListChanged: true})
		if err := subs.acknowledge(ack, func(transport.SubscriptionAck) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("write failed")
	calls := 0
	err := subs.forEach(func(json.RawMessage) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("forEach error = %v after %d calls, want the first error after 1 call", err, calls)
	}
}

func TestProxySession_listenGoesToDaemonUntilItCanBeAnsweredLocally(t *testing.T) {
	tests := map[string]struct {
		line  []byte
		ready bool
	}{
		"client not ready": {listenLine(5), false},
		"legacy version": {transporttest.ModernRequestVersion(transporttest.ModernRequestVersionParams{
			Version: transport.LatestLegacyVersion, ID: 5, Method: transport.MethodSubscriptionsListen,
		}), true},
		"null id": {bytes.Replace(listenLine(5), []byte(`"id":5`), []byte(`"id":null`), 1), true},
		"no id":   {bytes.Replace(listenLine(5), []byte(`"id":5,`), nil, 1), true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			session, out := newTestProxySession(t)
			session.clientReady.Store(tc.ready)
			handled, err := handleLocal(t, session, tc.line)
			if err != nil || handled || out.Len() != 0 || proxyListenCount(session) != 0 {
				t.Fatalf("handled=%t error=%v output=%q listens=%d, want it left for the daemon",
					handled, err, out.String(), proxyListenCount(session))
			}
		})
	}
}

//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

type localDaemonTransport struct {
	mu          sync.Mutex
	methods     []string
	initializes int
	compact     bool
}

func (d *localDaemonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		return localDaemonResponse(http.StatusNoContent, ""), nil
	}
	request, err := readDaemonRequest(req)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.methods = append(d.methods, request.Method)
	if request.Method == "initialize" {
		d.initializes++
		d.compact = request.Params.ToolMode == transport.ToolModeCompactValue
	}
	compact := d.compact
	d.mu.Unlock()
	if request.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	if request.Method == "tools/list" && compact {
		return daemonResult(request.ID, transport.ToolsListResult{Tools: []transport.ToolDefinition{
			{Name: "list"}, {Name: "call"}, {Name: "perm_call"}, {Name: "config"},
		}})
	}
	return daemonResult(request.ID, json.RawMessage(`{"ok":true}`))
}

func modernInput(lines ...[]byte) io.Reader {
	return bytes.NewReader(bytes.Join(lines, nil))
}

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
		JSONRPC: "2.0", Method: transport.NotificationCancelled, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func decodeLine[T any](t *testing.T, line []byte) T {
	t.Helper()
	var message T
	if err := json.Unmarshal(bytes.TrimSpace(line), &message); err != nil {
		t.Fatalf("decode %s: %v", line, err)
	}
	return message
}

func TestRun_modernRequestsHandshakeOnce(t *testing.T) {
	daemon := &localDaemonTransport{}
	requests := [][]byte{
		transporttest.ModernRequest(1, "server/discover", nil),
		transporttest.ModernRequest(2, "tools/list", nil),
	}
	for id := 3; id < 14; id++ {
		requests = append(requests, transporttest.ModernRequest(id, "tools/call", nil))
	}
	var out bytes.Buffer
	err := Run(RunParams{
		Client: &http.Client{Transport: daemon}, SessionID: "sess", In: modernInput(requests...), Out: &out,
		ToolMode: transport.ToolModeProxy, Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	if daemon.initializes != 1 {
		t.Fatalf("initialize requests = %d, want 1; methods %v", daemon.initializes, daemon.methods)
	}
	for _, method := range []string{"server/discover", "tools/list", "tools/call"} {
		if !slices.Contains(daemon.methods, method) {
			t.Errorf("daemon did not receive %s; methods %v", method, daemon.methods)
		}
	}
}

func TestRun_modernCompactModeUsesHandshakeToolMode(t *testing.T) {
	daemon := &localDaemonTransport{}
	var out bytes.Buffer
	err := Run(RunParams{
		Client: &http.Client{Transport: daemon}, SessionID: "sess", Out: &out,
		In:       modernInput(transporttest.ModernRequest(1, "tools/list", nil)),
		ToolMode: transport.ToolModeCompact, Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	daemon.mu.Lock()
	compact := daemon.compact
	daemon.mu.Unlock()
	if !compact || !strings.Contains(out.String(), `"name":"perm_call"`) {
		t.Fatalf("compact handshake=%t tools/list output=%q", compact, out.String())
	}
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

func TestProxySession_modernListenWithWrongJSONRPCGoesToDaemon(t *testing.T) {
	session, out := newTestProxySession(t, transport.ToolModeProxy)
	listen := bytes.Replace(listenLine(5), []byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1)
	handled, err := handleLocal(t, session, listen)
	if err != nil || handled || out.Len() != 0 || proxyListenCount(session) != 0 {
		t.Fatalf("handled=%t error=%v output=%q listens=%d", handled, err, out.String(), proxyListenCount(session))
	}
}

func newTestProxySession(t *testing.T, toolMode transport.ToolMode) (*proxySession, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	session := newProxySession(RunParams{ToolMode: toolMode, Clock: clock.NewFake()}, newLineWriter(&out))
	t.Cleanup(session.close)
	return session, &out
}

// newReadyProxySession skips the handshake a first modern request would forward to the daemon.
func newReadyProxySession(t *testing.T, toolMode transport.ToolMode) (*proxySession, *bytes.Buffer) {
	t.Helper()
	session, out := newTestProxySession(t, toolMode)
	session.clientReady.Store(true)
	return session, out
}

func handleLocal(t *testing.T, session *proxySession, line []byte) (bool, error) {
	t.Helper()
	return session.handleLocalModernLine(decodeLine[modernLine](t, line))
}

func toolsChangedLine(t *testing.T) json.RawMessage {
	t.Helper()
	line, err := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationToolsChanged})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func proxyListenCount(session *proxySession) int {
	count := 0
	_ = session.subs.forEach(func(json.RawMessage) error { //nolint:errcheck // the callback never fails.
		count++
		return nil
	})
	return count
}

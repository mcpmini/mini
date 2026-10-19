//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
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
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var message requestID
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, err
	}
	var method struct {
		Method string `json:"method"`
		Params struct {
			ToolMode string `json:"_mini_tool_mode"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &method); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.methods = append(d.methods, method.Method)
	if method.Method == "initialize" {
		d.initializes++
		d.compact = method.Params.ToolMode == transport.ToolModeCompactValue
	}
	compact := d.compact
	d.mu.Unlock()
	if method.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	result := `{"ok":true}`
	if method.Method == "tools/list" && compact {
		result = `{"tools":[{"name":"list"},{"name":"call"},{"name":"perm_call"},{"name":"config"}]}`
	}
	return localDaemonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":`+string(message.ID)+`,"result":`+result+`}`), nil
}

func localDaemonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestRun_modernRequestsHandshakeOnce(t *testing.T) {
	daemon := &localDaemonTransport{}
	client := &http.Client{Transport: daemon}
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
	}
	for id := 4; id < 14; id++ {
		requests = append(requests, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			id,
		))
	}
	lines := strings.Join(requests, "\n") + "\n"
	var out bytes.Buffer
	err := Run(RunParams{
		Client: client, SessionID: "sess", In: strings.NewReader(lines), Out: &out,
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
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}` + "\n"
	var out bytes.Buffer
	err := Run(RunParams{
		Client: &http.Client{Transport: daemon}, SessionID: "sess", In: strings.NewReader(line), Out: &out,
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
	var out bytes.Buffer
	session := newProxySession(
		RunParams{ToolMode: transport.ToolModeProxy, Clock: clock.NewFake()},
		newLineWriter(&out),
	)
	defer session.close()
	session.subs.modern.Store(true)
	session.clientReady.Store(true)
	listen := []byte(
		`{"jsonrpc":"2.0","id":5,"method":"subscriptions/listen","params":{"_meta":` +
			`{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":` +
			`{"toolsListChanged":true}}}`,
	)
	handled, err := session.handleLocalModernLine(listen)
	if err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	var ack map[string]any
	if err = json.Unmarshal(out.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	params := ack["params"].(map[string]any)
	if ack["method"] != "notifications/subscriptions/acknowledged" ||
		params["_meta"].(map[string]any)[transport.SubscriptionIDMeta] != float64(5) ||
		params["notifications"].(map[string]any)["toolsListChanged"] != true {
		t.Fatalf("listen acknowledgment = %v", ack)
	}
	if got := proxyListenCount(session); got != 1 {
		t.Fatalf("registered listens = %d, want 1", got)
	}
	cancel := []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}`)
	handled, err = session.handleLocalModernLine(cancel)
	if err != nil || !handled || proxyListenCount(session) != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf(
			"cancel handled=%t error=%v listens=%d output=%q",
			handled, err, proxyListenCount(session), out.String(),
		)
	}
	if err = session.notificationStream.forward(json.RawMessage(
		`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`,
	)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("notification arrived after cancel: %q", out.String())
	}
	unknown := []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":6}}`)
	handled, err = session.handleLocalModernLine(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("unknown cancellation was consumed")
	}
}

func TestProxySession_modernListenCompactAcknowledgesEmptyFilter(t *testing.T) {
	var out bytes.Buffer
	session := newProxySession(
		RunParams{ToolMode: transport.ToolModeCompact, Clock: clock.NewFake()},
		newLineWriter(&out),
	)
	defer session.close()
	session.subs.modern.Store(true)
	session.clientReady.Store(true)
	line := []byte(
		`{"jsonrpc":"2.0","id":"listen","method":"subscriptions/listen","params":{"_meta":` +
			`{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":` +
			`{"toolsListChanged":true}}}`,
	)
	if handled, err := session.handleLocalModernLine(line); err != nil || !handled {
		t.Fatalf("listen handled=%t error=%v", handled, err)
	}
	var ack map[string]any
	if err := json.Unmarshal(out.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	if notifications := ack["params"].(map[string]any)["notifications"].(map[string]any); len(notifications) != 0 {
		t.Fatalf("compact acknowledgment notifications = %v", notifications)
	}
	if got := proxyListenCount(session); got != 0 {
		t.Fatalf("compact registered listens = %d, want 0", got)
	}
}

func TestNotificationStream_modernTaggingAndLegacyForwarding(t *testing.T) {
	var out bytes.Buffer
	session := newProxySession(
		RunParams{ToolMode: transport.ToolModeProxy, Clock: clock.NewFake()},
		newLineWriter(&out),
	)
	defer session.close()
	notification := json.RawMessage(
		`{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{"_meta":` +
			`{"existing":"kept"}}}`,
	)
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
	var tagged map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &tagged); err != nil {
		t.Fatal(err)
	}
	meta := tagged["params"].(map[string]any)["_meta"].(map[string]any)
	if meta["existing"] != "kept" || meta[transport.SubscriptionIDMeta] != float64(9) {
		t.Fatalf("tagged notification meta = %v", meta)
	}
}

func proxyListenCount(session *proxySession) int {
	count := 0
	_ = session.subs.forEach(func(json.RawMessage) error { //nolint:errcheck // the callback never fails.
		count++
		return nil
	})
	return count
}

func TestProxySession_modernListenWithWrongJSONRPCGoesToDaemon(t *testing.T) {
	var out bytes.Buffer
	session := newProxySession(
		RunParams{ToolMode: transport.ToolModeProxy, Clock: clock.NewFake()},
		newLineWriter(&out),
	)
	defer session.close()
	listen := []byte(
		`{"jsonrpc":"1.0","id":5,"method":"subscriptions/listen","params":{"_meta":` +
			`{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":` +
			`{"toolsListChanged":true}}}`,
	)
	handled, err := session.handleLocalModernLine(listen)
	if err != nil || handled || out.Len() != 0 || proxyListenCount(session) != 0 {
		t.Fatalf("handled=%t error=%v output=%q listens=%d", handled, err, out.String(), proxyListenCount(session))
	}
}

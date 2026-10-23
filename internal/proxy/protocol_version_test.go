//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
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
	}
	d.mu.Unlock()
	if request.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	return daemonResult(request.ID, json.RawMessage(`{"ok":true}`))
}

func modernInput(lines ...[]byte) io.Reader {
	return bytes.NewReader(bytes.Join(lines, nil))
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
		Clock: clock.NewFake(),
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

func newTestProxySession(t *testing.T) (*proxySession, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	session := newProxySession(RunParams{Clock: clock.NewFake()}, newLineWriter(&out))
	t.Cleanup(session.close)
	return session, &out
}

// newReadyProxySession skips the handshake a first modern request would forward to the daemon.
func newReadyProxySession(t *testing.T) (*proxySession, *bytes.Buffer) {
	t.Helper()
	session, out := newTestProxySession(t)
	session.clientReady.Store(true)
	return session, out
}

func TestRun_rejectsMalformedModernMetadataBeforeBootstrapOrListenAck(t *testing.T) {
	daemon := &localDaemonTransport{}
	line := []byte(
		`{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen",` +
			`"params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},` +
			`"notifications":{"tools/list_changed":true}}}`,
	)
	var out bytes.Buffer
	err := Run(RunParams{
		Client: &http.Client{Transport: daemon}, In: modernInput(line), Out: &out,
		Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	response := decodeLine[transport.Response](t, out.Bytes())
	if response.Error == nil || response.Error.Code != transport.CodeInvalidParams || response.ID != float64(7) {
		t.Fatalf("invalid metadata response = %s", out.Bytes())
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	if daemon.initializes != 0 || len(daemon.methods) != 0 {
		t.Fatalf("invalid listen bootstrapped daemon: initialize=%d methods=%v", daemon.initializes, daemon.methods)
	}
}

func TestProxySession_rejectsMalformedModernDiscoverWithoutPoisoningBridge(t *testing.T) {
	daemon := &localDaemonTransport{}
	var out bytes.Buffer
	session := newProxySession(RunParams{
		Client: &http.Client{Transport: daemon}, Clock: clock.NewFake(),
	}, newLineWriter(&out))
	defer session.close()

	malformed := []byte(
		`{"jsonrpc":"2.0","id":1,"method":"server/discover",` +
			`"params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
	)
	handled, err := session.handleModernInput(malformed)
	if err != nil || !handled {
		t.Fatalf("malformed discover handled=%t error=%v", handled, err)
	}
	response := decodeLine[transport.Response](t, out.Bytes())
	if response.Error == nil || response.Error.Code != transport.CodeInvalidParams || response.ID != float64(1) {
		t.Fatalf("malformed discover response = %s", out.Bytes())
	}
	daemon.mu.Lock()
	bootstrapped := daemon.initializes != 0 || len(daemon.methods) != 0
	methods := slices.Clone(daemon.methods)
	daemon.mu.Unlock()
	if bootstrapped || session.clientReady.Load() {
		t.Fatalf("malformed discover changed bridge readiness: methods=%v ready=%t",
			methods, session.clientReady.Load())
	}

	valid := transporttest.ModernRequest(2, "server/discover", nil)
	handled, err = session.handleModernInput(valid)
	if err != nil || handled {
		t.Fatalf("valid discover handled=%t error=%v", handled, err)
	}
	if response := session.forward(classifyForwardedMessage(valid)); response != nil {
		if err := session.writeLine(response); err != nil {
			t.Fatalf("write valid discover response: %v", err)
		}
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("responses = %q, want malformed error and valid result", out.Bytes())
	}
	validResponse := decodeLine[transport.Response](t, lines[1])
	if validResponse.Error != nil || validResponse.ID != float64(2) {
		t.Fatalf("valid discover response = %s", lines[1])
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	if daemon.initializes != 1 || !slices.Contains(daemon.methods, "server/discover") {
		t.Fatalf("valid discover did not use ready bridge: initialize=%d methods=%v",
			daemon.initializes, daemon.methods)
	}
}

func TestProxySession_modernInputLeftForTheDaemon(t *testing.T) {
	invalidMeta := `"params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`
	tests := map[string]struct {
		line   string
		modern bool
	}{
		"malformed JSON":             {`{"jsonrpc":`, false},
		"legacy request":             {`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, false},
		"notification with bad meta": {`{"jsonrpc":"2.0","method":"notifications/progress",` + invalidMeta + `}`, true},
		"null id with bad meta":      {`{"jsonrpc":"2.0","id":null,"method":"tools/list",` + invalidMeta + `}`, true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			session, out := newReadyProxySession(t)
			handled, err := session.handleModernInput([]byte(tc.line))
			if err != nil || handled || out.Len() != 0 {
				t.Fatalf("handled=%t error=%v output=%q, want the line forwarded untouched", handled, err, out.String())
			}
			if got := session.subs.modern.Load(); got != tc.modern {
				t.Fatalf("session modern = %t, want %t", got, tc.modern)
			}
		})
	}
}

func TestProxySession_modernRequestOnAReadySessionMarksItModern(t *testing.T) {
	session, out := newReadyProxySession(t)
	handled, err := session.handleModernInput(transporttest.ModernRequest(1, "tools/list", nil))
	if err != nil || handled || out.Len() != 0 {
		t.Fatalf("handled=%t error=%v output=%q, want the request forwarded", handled, err, out.String())
	}
	if !session.subs.modern.Load() {
		t.Fatal("modern request did not switch notification forwarding to modern")
	}
}

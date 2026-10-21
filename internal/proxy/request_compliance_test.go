//go:build test

package proxy

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

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
		ToolMode: transport.ToolModeProxy, Clock: clock.NewFake(),
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
		Client: &http.Client{Transport: daemon}, ToolMode: transport.ToolModeProxy, Clock: clock.NewFake(),
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

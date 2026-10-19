//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

type compactRecoveryTransport struct {
	initializes int
	calls       int
	compact     bool
}

func (d *compactRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	request, err := readDaemonRequest(req)
	if err != nil {
		return nil, err
	}
	switch request.Method {
	case "initialize":
		d.initializes++
		d.compact = request.Params.ToolMode == transport.ToolModeCompactValue
	case transport.NotificationInitialized:
		return localDaemonResponse(http.StatusAccepted, ""), nil
	case "tools/call":
		d.calls++
		if d.calls == 1 {
			return daemonResponse(transport.Response{JSONRPC: "2.0", ID: request.ID, Error: &transport.RPCError{
				Code: transport.CodeInvalidRequest, Message: transport.NotInitializedMessage,
			}})
		}
	}
	return daemonResult(request.ID, json.RawMessage(`{"data":{"answer":"ok"}}`))
}

func TestRun_modernCompactCallRecoversAfterIdleEviction(t *testing.T) {
	daemon := &compactRecoveryTransport{}
	fakeClock := clock.NewFake()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: modernInput(transporttest.ModernRequest(1, "tools/call", nil)), Out: &out,
			ToolMode: transport.ToolModeCompact, Clock: fakeClock,
		})
	}()
	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(recoveryBackoff)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if daemon.initializes != 2 || !daemon.compact || !strings.Contains(out.String(), `"data":{"answer":"ok"}`) {
		t.Fatalf("initializes=%d compact=%t output=%q", daemon.initializes, daemon.compact, out.String())
	}
}

type restartingDaemonTransport struct {
	resolved    *atomic.Int32
	initializes int
}

func (d *restartingDaemonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if d.resolved.Load() == 0 {
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}
	request, err := readDaemonRequest(req)
	if err != nil {
		return nil, err
	}
	if request.Method == "initialize" {
		d.initializes++
	}
	if request.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	return daemonResult(request.ID, transport.ToolsListResult{Tools: []transport.ToolDefinition{{Name: "call"}}})
}

func TestRun_modernAgentRecoversAfterDaemonRestart(t *testing.T) {
	var resolves atomic.Int32
	daemon := &restartingDaemonTransport{resolved: &resolves}
	fakeClock := clock.NewFake()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: modernInput(transporttest.ModernRequest(1, "tools/list", nil)), Out: &out,
			ToolMode: transport.ToolModeCompact, Clock: fakeClock,
			Resolver: NewDaemonResolver(func() (string, error) {
				resolves.Add(1)
				return "restarted", nil
			}),
		})
	}()
	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(recoveryBackoff)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolves.Load() != 1 || daemon.initializes != 1 || !strings.Contains(out.String(), `"name":"call"`) {
		t.Fatalf("resolves=%d initializes=%d output=%q", resolves.Load(), daemon.initializes, out.String())
	}
}

func TestRun_modernListenFirstRecoversDaemonBeforeAcknowledging(t *testing.T) {
	var resolves atomic.Int32
	daemon := &restartingDaemonTransport{resolved: &resolves}
	fakeClock := clock.NewFake()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: modernInput(listenLine(1)),
			Out: &out, ToolMode: transport.ToolModeCompact, Clock: fakeClock,
			Resolver: NewDaemonResolver(func() (string, error) {
				resolves.Add(1)
				return "restarted", nil
			}),
		})
	}()
	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(recoveryBackoff)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolves.Load() != 1 || daemon.initializes != 1 ||
		!strings.Contains(out.String(), "notifications/subscriptions/acknowledged") {
		t.Fatalf("resolves=%d initializes=%d output=%q", resolves.Load(), daemon.initializes, out.String())
	}
}

func TestRun_modernListenFirstWithUnreachableDaemonIsNotAcknowledged(t *testing.T) {
	var resolves atomic.Int32
	daemon := &restartingDaemonTransport{resolved: &resolves}
	var out bytes.Buffer
	err := Run(RunParams{
		Client: &http.Client{Transport: daemon}, SessionID: "sess", In: modernInput(listenLine(1)),
		Out: &out, ToolMode: transport.ToolModeCompact, Clock: clock.NewFake(),
		Resolver: NewDaemonResolver(func() (string, error) { return "", errors.New("no daemon") }),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "acknowledged") || !strings.Contains(out.String(), `"error"`) {
		t.Fatalf("listen with unreachable daemon output = %q, want an error and no acknowledgement", out.String())
	}
}

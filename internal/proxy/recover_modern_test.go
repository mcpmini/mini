//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

type compactRecoveryTransport struct {
	initializes int
	calls       int
	compact     bool
}

func (d *compactRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ToolMode string `json:"_mini_tool_mode"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, err
	}
	if message.Method == "initialize" {
		d.initializes++
		d.compact = message.Params.ToolMode == transport.ToolModeCompactValue
	}
	if message.Method == "tools/call" {
		d.calls++
		if d.calls == 1 {
			return localDaemonResponse(
				http.StatusOK,
				`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"`+
					transport.NotInitializedMessage+`"}}`,
			), nil
		}
	}
	if message.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	return localDaemonResponse(
		http.StatusOK,
		`{"jsonrpc":"2.0","id":`+string(message.ID)+`,"result":{"data":{"answer":"ok"}}}`,
	), nil
}

func TestRun_modernCompactCallRecoversAfterIdleEviction(t *testing.T) {
	daemon := &compactRecoveryTransport{}
	fakeClock := clock.NewFake()
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}` + "\n"
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: strings.NewReader(line), Out: &out,
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
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, err
	}
	if message.Method == "initialize" {
		d.initializes++
	}
	if message.Method == transport.NotificationInitialized {
		return localDaemonResponse(http.StatusAccepted, ""), nil
	}
	return localDaemonResponse(
		http.StatusOK,
		`{"jsonrpc":"2.0","id":`+string(message.ID)+`,"result":{"tools":[{"name":"call"}]}}`,
	), nil
}

func TestRun_modernAgentRecoversAfterDaemonRestart(t *testing.T) {
	var resolves atomic.Int32
	daemon := &restartingDaemonTransport{resolved: &resolves}
	fakeClock := clock.NewFake()
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}` + "\n"
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: strings.NewReader(line), Out: &out,
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

const modernListenFirstLine = `{"jsonrpc":"2.0","id":1,"method":"subscriptions/listen","params":{"_meta":` +
	`{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":{"toolsListChanged":true}}}` + "\n"

func TestRun_modernListenFirstRecoversDaemonBeforeAcknowledging(t *testing.T) {
	var resolves atomic.Int32
	daemon := &restartingDaemonTransport{resolved: &resolves}
	fakeClock := clock.NewFake()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: &http.Client{Transport: daemon}, SessionID: "sess", In: strings.NewReader(modernListenFirstLine),
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
		Client: &http.Client{Transport: daemon}, SessionID: "sess", In: strings.NewReader(modernListenFirstLine),
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

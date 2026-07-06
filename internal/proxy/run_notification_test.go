//go:build test

package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

const (
	proxyInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	proxyReady      = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	proxyToolCall   = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`
)

type fakeNotificationDaemon struct {
	t             *testing.T
	requiredToken string
	streamOpened  chan struct{}
	errs          chan error
}

func newFakeNotificationDaemon(t *testing.T, requiredToken string) (*fakeNotificationDaemon, *http.Client) {
	t.Helper()
	daemon := &fakeNotificationDaemon{
		t: t, requiredToken: requiredToken,
		streamOpened: make(chan struct{}, 1), errs: make(chan error, 4),
	}
	return daemon, serveSocket(t, daemon.serveHTTP)
}

func (d *fakeNotificationDaemon) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		d.serveStream(w, r)
		return
	}
	msg, err := readProxyMessage(r)
	if err != nil {
		d.recordError(err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	switch msg.Method {
	case "initialize":
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26"}}`, msg.ID)
	case transport.NotificationInitialized:
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		if d.authorize(w, r) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, msg.ID)
		}
	default:
		d.recordError(fmt.Errorf("unexpected POST method %q", msg.Method))
		http.Error(w, "unexpected method", http.StatusBadRequest)
	}
}

func (d *fakeNotificationDaemon) serveStream(w http.ResponseWriter, r *http.Request) {
	if !d.authorize(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
	select {
	case d.streamOpened <- struct{}{}:
	default:
	}
}

func (d *fakeNotificationDaemon) authorize(w http.ResponseWriter, r *http.Request) bool {
	if d.requiredToken == "" || r.Header.Get("Authorization") == "Bearer "+d.requiredToken {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func (d *fakeNotificationDaemon) waitForStream() {
	d.t.Helper()
	select {
	case <-d.streamOpened:
	case <-time.After(2 * time.Second):
		d.t.Fatal("timed out waiting for notification stream")
	}
}

func (d *fakeNotificationDaemon) recordError(err error) {
	select {
	case d.errs <- err:
	default:
	}
}

func (d *fakeNotificationDaemon) assertNoError() {
	d.t.Helper()
	select {
	case err := <-d.errs:
		d.t.Error(err)
	default:
	}
}

type proxyMessage struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
}

func readProxyMessage(r *http.Request) (proxyMessage, error) {
	var msg proxyMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		return proxyMessage{}, err
	}
	return msg, nil
}

type proxyRunConfig struct {
	token    string
	resolver *DaemonResolver
}

type proxyRunHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	out   *safeBuffer
	done  chan error
	close sync.Once
}

func startProxyRun(t *testing.T, client *http.Client, cfg proxyRunConfig) *proxyRunHarness {
	t.Helper()
	inR, inW := io.Pipe()
	h := &proxyRunHarness{t: t, in: inW, out: &safeBuffer{}, done: make(chan error, 1)}
	go func() {
		h.done <- Run(RunParams{
			Client: client, SessionID: "sess", Token: cfg.token, In: inR, Out: h.out,
			Resolver: cfg.resolver, Clock: clock.System(),
		})
	}()
	t.Cleanup(h.Close)
	return h
}

func (h *proxyRunHarness) initialize() {
	h.send(proxyInitialize, proxyReady)
}

func (h *proxyRunHarness) send(lines ...string) {
	h.t.Helper()
	for _, line := range lines {
		if _, err := fmt.Fprintln(h.in, line); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *proxyRunHarness) waitForOutput(want string) {
	h.t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(h.out.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %q in output: %q", want, h.out.String())
}

func (h *proxyRunHarness) Close() {
	h.close.Do(func() {
		h.in.Close()
		if err := <-h.done; err != nil {
			h.t.Errorf("Run error: %v", err)
		}
	})
}

func TestRun_relaysDaemonNotificationsAfterInitialized(t *testing.T) {
	daemon, client := newFakeNotificationDaemon(t, "")
	run := startProxyRun(t, client, proxyRunConfig{})
	run.initialize()
	daemon.waitForStream()
	run.waitForOutput(`"method":"notifications/tools/list_changed"`)
	run.Close()
	run.waitForOutput(`"id":1`)
	daemon.assertNoError()
}

func TestRun_reopensNotificationStreamAfterRecovery(t *testing.T) {
	daemon, client := newFakeNotificationDaemon(t, "fresh")
	run := startProxyRun(t, client, proxyRunConfig{
		token:    "stale",
		resolver: NewDaemonResolver(func() (string, error) { return "fresh", nil }),
	})
	run.initialize()
	run.send(proxyToolCall)
	daemon.waitForStream()
	run.waitForOutput(`"result":"ok"`)
	run.waitForOutput(`"method":"notifications/tools/list_changed"`)
	run.Close()
	daemon.assertNoError()
}

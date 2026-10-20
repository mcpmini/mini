//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestStdioSubscriptionIgnoresChangesBeforeItsAck(t *testing.T) {
	h := startListenHarness(t)
	h.ack("wrong")
	h.toolsChanged(h.listenID())
	h.expectNoChange()
}

func TestStdioSubscriptionAckRefreshesTools(t *testing.T) {
	h := startListenHarness(t)
	h.ack(h.listen.ID)
	h.expectChange()
}

func TestStdioSubscriptionForwardsOnlyItsOwnChanges(t *testing.T) {
	h := startAcknowledgedListen(t)
	h.toolsChanged(json.RawMessage(`"other"`))
	h.expectNoChange()
	h.toolsChanged(h.listenID())
	h.expectChange()
}

func TestStdioSubscriptionEndedByTheServerRefreshesOnceAndStops(t *testing.T) {
	for name, end := range map[string]func(h *listenHarness){
		"graceful completion": func(h *listenHarness) {
			sendResponse(h.serverW, h.listen.ID, json.RawMessage(`{"resultType":"complete"}`))
		},
		"server cancellation": func(h *listenHarness) { h.serverCancels(h.listenID()) },
	} {
		t.Run(name, func(t *testing.T) {
			h := startAcknowledgedListen(t)
			end(h)
			h.expectChange()
			h.expectStopped()
		})
	}
}

func TestStdioSubscriptionIgnoresAnUnrelatedCancellation(t *testing.T) {
	h := startAcknowledgedListen(t)
	h.serverCancels(json.RawMessage(`"unrelated"`))
	h.expectRunning()
}

func TestStdioSubscriptionWithoutAckCancelsTheListenAndStops(t *testing.T) {
	h := startListenHarness(t)
	advanceStdioTimer(t, h.clock, stdioListenAckTimeout)
	cancel := readScannerRequest(t, h.scanner)
	var params CancelledParams
	if cancel.Method != NotificationCancelled || json.Unmarshal(cancel.Params, &params) != nil ||
		!sameJSONID(params.RequestID, h.listen.ID) {
		t.Fatalf(
			"after the ack timeout mini sent %q %s, want a cancel for listen %v",
			cancel.Method,
			cancel.Params,
			h.listen.ID,
		)
	}
	h.expectStopped()
}

func TestStdioSubscriptionRejectionStopsTheListener(t *testing.T) {
	h := startAcknowledgedListen(t)
	writeRPCError(h.serverW, h.listen.ID, CodeInternalError, "listen rejected", nil)
	h.expectStopped()
}

type listenHarness struct {
	t       *testing.T
	conn    *StdioConnection
	serverW *io.PipeWriter
	scanner *bufio.Scanner
	clock   *recordingStdioClock
	changes chan Notification
	listen  Request
}

func startListenHarness(t *testing.T) *listenHarness {
	t.Helper()
	conn, serverW, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	h := &listenHarness{
		t: t, conn: conn, serverW: serverW, scanner: bufio.NewScanner(serverR),
		clock:   &recordingStdioClock{Fake: clock.NewFake(), timers: make(chan time.Duration, 8)},
		changes: make(chan Notification, 4),
	}
	conn.clock = h.clock
	conn.SetNotificationHandler(func(notification Notification) { h.changes <- notification })
	conn.startSubscriptionListener(context.Background())
	t.Cleanup(func() { conn.Close() }) //nolint:errcheck // the child's exit status is irrelevant to these tests.
	if h.listen = readScannerRequest(t, h.scanner); h.listen.Method != MethodSubscriptionsListen {
		t.Fatalf("first request = %q, want %s", h.listen.Method, MethodSubscriptionsListen)
	}
	return h
}

func startAcknowledgedListen(t *testing.T) *listenHarness {
	t.Helper()
	h := startListenHarness(t)
	h.ack(h.listen.ID)
	h.expectChange()
	return h
}

func (h *listenHarness) listenID() json.RawMessage { return mustJSONID(h.t, h.listen.ID) }

func (h *listenHarness) ack(id any) { ackListen(h.t, h.serverW, id) }

func (h *listenHarness) toolsChanged(subscriptionID json.RawMessage) {
	params := SubscriptionNotificationParams{Meta: SubscriptionMeta{SubscriptionID: subscriptionID}}
	writeNotification(h.t, h.serverW, NotificationToolsChanged, mustJSON(h.t, params))
}

func (h *listenHarness) serverCancels(requestID json.RawMessage) {
	writeNotification(h.t, h.serverW, NotificationCancelled, mustJSON(h.t, CancelledParams{RequestID: requestID}))
}

func (h *listenHarness) expectChange() {
	h.t.Helper()
	if got := receiveChangedNotification(h.t, h.changes); got.Method != NotificationToolsChanged {
		h.t.Fatalf("change event = %q, want %s", got.Method, NotificationToolsChanged)
	}
}

// The reader handles messages in order, so once the barrier call returns, any change written
// before it has already reached the handler.
func (h *listenHarness) expectNoChange() {
	h.t.Helper()
	barrierCall(h.t, h.conn, h.serverW, h.scanner)
	select {
	case got := <-h.changes:
		h.t.Fatalf("unexpected %s reached the handler", got.Method)
	default:
	}
}

func (h *listenHarness) expectStopped() {
	h.t.Helper()
	select {
	case <-h.conn.listenDone:
	case <-time.After(time.Second):
		h.t.Fatal("the listener is still running")
	}
}

func (h *listenHarness) expectRunning() {
	h.t.Helper()
	barrierCall(h.t, h.conn, h.serverW, h.scanner)
	select {
	case <-h.conn.listenDone:
		h.t.Fatal("the listener stopped")
	default:
	}
}

func ackListen(t *testing.T, w interface{ Write([]byte) (int, error) }, id any) {
	t.Helper()
	writeNotification(t, w, NotificationSubscriptionsAcknowledged, mustJSON(t, SubscriptionAckParams{
		Meta:          SubscriptionMeta{SubscriptionID: mustJSONID(t, id)},
		Notifications: ListenNotifications{ToolsListChanged: true},
	}))
}

func barrierCall(
	t *testing.T,
	conn *StdioConnection,
	serverW interface{ Write([]byte) (int, error) },
	scanner *bufio.Scanner,
) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(context.Background(), "ping", nil)
		done <- err
	}()
	ping := readScannerRequest(t, scanner)
	sendResponse(serverW, ping.ID, json.RawMessage(`{}`))
	if err := <-done; err != nil {
		t.Fatalf("barrier ping: %v", err)
	}
}

type recordingStdioClock struct {
	*clock.Fake
	timers chan time.Duration
}

func (c *recordingStdioClock) NewTimer(duration time.Duration) clock.Timer {
	timer := c.Fake.NewTimer(duration)
	c.timers <- duration
	return timer
}

func advanceStdioTimer(t *testing.T, fakeClock *recordingStdioClock, duration time.Duration) {
	t.Helper()
	waitStdioTimer(t, fakeClock, duration)
	fakeClock.Advance(duration)
}

func waitStdioTimer(t *testing.T, fakeClock *recordingStdioClock, want time.Duration) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-fakeClock.timers:
			if got == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for timer %s", want)
		}
	}
}

func mustJSONID(t *testing.T, id any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func writeNotification(t *testing.T, w interface{ Write([]byte) (int, error) }, method string, params json.RawMessage) {
	t.Helper()
	line, err := json.Marshal(Notification{JsonRPC: JsonRPCVersion, Method: method, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", line); err != nil {
		t.Fatal(err)
	}
}

func receiveChangedNotification(t *testing.T, changes <-chan Notification) Notification {
	t.Helper()
	select {
	case notification := <-changes:
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool-change notification")
		return Notification{}
	}
}

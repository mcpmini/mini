//go:build test

package server

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestSessionNotifyRetainsChangeUntilStreamOpens(t *testing.T) {
	session := newSession(clock.NewFake())
	session.setToolModeOnce(transport.ToolModeProxy)
	session.markInitialized()
	session.markClientReady()
	session.notify(toolsChangedNotif)

	stream, ok := session.enableNotifications()
	if !ok {
		t.Fatal("enableNotifications() = closed")
	}
	if got := <-stream; string(got) != string(toolsChangedNotif) {
		t.Fatalf("notification = %s", got)
	}
	select {
	case got := <-stream:
		t.Fatalf("duplicate notification = %s", got)
	default:
	}
}

func TestSessionNotificationsCoalescesPendingChanges(t *testing.T) {
	n := newSessionNotifications()
	first := json.RawMessage(`{"change":1}`)
	latest := json.RawMessage(`{"change":2}`)
	if !n.publish(first) || !n.publish(latest) {
		t.Fatal("publish() dropped pending change")
	}
	stream, ok := n.open()
	if !ok {
		t.Fatal("open() = closed")
	}
	if got := <-stream; string(got) != string(latest) {
		t.Fatalf("notification = %s, want %s", got, latest)
	}
	select {
	case got := <-stream:
		t.Fatalf("second notification = %s", got)
	default:
	}
}

func TestSessionNotifications_closeAllPreventsFutureOpen(t *testing.T) {
	n := newSessionNotifications()
	stream, ok := n.open()
	if !ok {
		t.Fatal("open() = closed before shutdown")
	}
	n.closeAll()

	if _, ok := n.open(); ok {
		t.Fatal("open() succeeded after closeAll")
	}
	if _, open := <-stream; open {
		t.Fatal("stream remained open after closeAll")
	}
}

func TestSessionNotifications_closeAllWinsAgainstConcurrentOpen(t *testing.T) {
	for range 64 {
		n := newSessionNotifications()
		start := make(chan struct{})
		type result struct {
			stream chan json.RawMessage
			ok     bool
		}
		done := make(chan result, 1)
		go func() {
			<-start
			stream, ok := n.open()
			done <- result{stream: stream, ok: ok}
		}()

		close(start)
		n.closeAll()
		res := <-done
		if res.ok {
			if _, open := <-res.stream; open {
				t.Fatal("concurrent open left a surviving stream")
			}
		}
		if _, ok := n.open(); ok {
			t.Fatal("open() succeeded after shutdown race")
		}
	}
}

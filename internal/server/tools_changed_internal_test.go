//go:build test

package server

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolsChangedOutboxDeliversPendingToNextStream(t *testing.T) {
	outbox := newToolsChangedOutbox()
	if !outbox.publishToolsChanged() {
		t.Fatal("expected publish with no streams to retain pending notification")
	}
	stream, ok := outbox.openStream()
	if !ok {
		t.Fatal("expected stream to open")
	}
	assertToolsChanged(t, stream)
	assertNoToolsChanged(t, stream)
}

func TestToolsChangedOutboxDeliversToOneOpenStream(t *testing.T) {
	outbox := newToolsChangedOutbox()
	first, _ := outbox.openStream()
	second, _ := outbox.openStream()

	if !outbox.publishToolsChanged() {
		t.Fatal("expected publish to open streams")
	}
	got := receiveCount(first) + receiveCount(second)
	if got != 1 {
		t.Fatalf("expected exactly one stream to receive notification, got %d", got)
	}
}

func assertToolsChanged(t *testing.T, stream <-chan json.RawMessage) {
	t.Helper()
	select {
	case got := <-stream:
		if string(got) != string(toolsChangedNotif) {
			t.Fatalf("notification = %s, want %s", got, toolsChangedNotif)
		}
	default:
		t.Fatal("expected tools/list_changed notification")
	}
}

func assertNoToolsChanged(t *testing.T, stream <-chan json.RawMessage) {
	t.Helper()
	select {
	case got := <-stream:
		t.Fatalf("unexpected notification: %s", got)
	default:
	}
}

func receiveCount(stream <-chan json.RawMessage) int {
	select {
	case <-stream:
		return 1
	default:
		return 0
	}
}

func TestToolChangeDuringListenAcknowledgementReachesTheListen(t *testing.T) {
	session := newSession(clock.System())
	session.modern.Store(true)
	var log messageLog
	writeAck, forwarded := forwardToolChangeDuringAck(session, log.record)
	listen := listenSubscription{
		id:            json.RawMessage(`7`),
		notifications: transport.ListenNotifications{ToolsListChanged: true},
	}
	session.acknowledgeListen(listen, func() {}, writeAck)
	<-forwarded
	written := log.messages()
	if len(written) != 2 {
		t.Fatalf("written = %+v, want the acknowledgement then one tagged tool change", written)
	}
	if _, tagged := written[1].(transport.ToolsListChangedNotification); !tagged {
		t.Fatalf("second message = %+v, want a tagged tool change", written[1])
	}
}

type messageLog struct {
	mu      sync.Mutex
	written []any
}

func (l *messageLog) record(message any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.written = append(l.written, message)
}

func (l *messageLog) messages() []any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.written)
}

func forwardToolChangeDuringAck(session *Session, record func(any)) (func(any), <-chan struct{}) {
	forwarded := make(chan struct{})
	return func(message any) {
		record(message)
		go func() {
			session.forwardToolsChanged(json.RawMessage(`{}`), record)
			close(forwarded)
		}()
		// Give a forwarder that is not held back by the acknowledgement time to run first.
		select {
		case <-forwarded:
		case <-time.After(50 * time.Millisecond):
		}
	}, forwarded
}

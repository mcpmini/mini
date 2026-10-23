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

func TestToolsChangedOutboxClose(t *testing.T) {
	outbox := newToolsChangedOutbox()
	open, _ := outbox.openStream()
	outbox.close()
	assertStreamClosed(t, open)
	if outbox.publishToolsChanged() {
		t.Fatal("publish after close reported delivery")
	}
	if stream, ok := outbox.openStream(); ok || stream != nil {
		t.Fatal("a stream opened on a closed outbox")
	}
}

func assertStreamClosed(t *testing.T, stream <-chan json.RawMessage) {
	t.Helper()
	select {
	case got, open := <-stream:
		if open {
			t.Fatalf("stream delivered %s, want it closed", got)
		}
	default:
		t.Fatal("stream is still open")
	}
}

func TestToolsChangedOutboxCloseStreamClosesOnlyThatStream(t *testing.T) {
	outbox := newToolsChangedOutbox()
	closing, _ := outbox.openStream()
	remaining, _ := outbox.openStream()
	outbox.closeStream(closing)
	outbox.closeStream(closing)
	assertStreamClosed(t, closing)
	outbox.publishToolsChanged()
	assertToolsChanged(t, remaining)
	outbox.closeStream(remaining)
	if outbox.hasOpenStream() {
		t.Fatal("outbox reports an open stream after closing every stream")
	}
}

func TestToolsChangedOutboxKeepsChangeWhenEveryStreamIsFull(t *testing.T) {
	outbox := newToolsChangedOutbox()
	full, _ := outbox.openStream()
	for range cap(full) {
		full <- toolsChangedNotif
	}
	if !outbox.publishToolsChanged() {
		t.Fatal("publish to a full stream was dropped")
	}
	next, _ := outbox.openStream()
	assertToolsChanged(t, next)
}

func TestNotifyToolsChangedWaitsForAReadyClient(t *testing.T) {
	session := newSession(clock.NewFake())
	session.notifyToolsChanged()
	stream, _ := session.openToolsChangedStream()
	assertNoToolsChanged(t, stream)
	session.clientReady.Store(true)
	session.notifyToolsChanged()
	assertToolsChanged(t, stream)
}

func TestNotifyAllSessionsReachesEverySession(t *testing.T) {
	srv := &Server{sessions: newSessionStore(clock.NewFake())}
	var streams []chan json.RawMessage
	for _, id := range []string{"a", "b"} {
		session := srv.sessions.getOrCreate(id)
		session.clientReady.Store(true)
		stream, _ := session.openToolsChangedStream()
		streams = append(streams, stream)
	}
	srv.notifyAllSessions()
	for _, stream := range streams {
		assertToolsChanged(t, stream)
	}
}

func TestSessionNotifyForwarder(t *testing.T) {
	t.Run("legacy session gets the bare notification", func(t *testing.T) {
		session := newSession(clock.NewFake())
		session.clientReady.Store(true)
		var log messageLog
		stop := startSessionNotifyForwarder(session, log.record)
		session.notifyToolsChanged()
		stop()
		written := log.messages()
		if len(written) != 1 || string(written[0].(json.RawMessage)) != string(toolsChangedNotif) {
			t.Fatalf("written = %v, want one bare tools/list_changed", written)
		}
		if session.toolsChanged.hasOpenStream() {
			t.Fatal("stop left the forwarder's stream open")
		}
	})
	t.Run("closed session forwards nothing", func(t *testing.T) {
		session := newSession(clock.NewFake())
		session.closeToolsChangedStreams()
		stop := startSessionNotifyForwarder(session, func(message any) { t.Errorf("forwarded %v", message) })
		stop()
	})
}

func TestForwardToolsChangedTagsOnlyToolListens(t *testing.T) {
	session := newSession(clock.NewFake())
	session.modern.Store(true)
	session.listenTools["1"] = true
	session.listenTools["2"] = false
	var log messageLog
	session.forwardToolsChanged(toolsChangedNotif, log.record)
	written := log.messages()
	if len(written) != 1 {
		t.Fatalf("written = %+v, want one tagged notification", written)
	}
	tagged, ok := written[0].(transport.ToolsListChangedNotification)
	if !ok || string(tagged.Params.Meta.SubscriptionID) != "1" {
		t.Fatalf("written = %+v, want a notification tagged for listen 1", written[0])
	}
}

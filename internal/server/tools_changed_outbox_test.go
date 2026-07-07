//go:build test

package server

import (
	"encoding/json"
	"testing"
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

//go:build test

package proxy

import "testing"

func TestDaemonConversationRestoreDoesNotRegressNotificationGeneration(t *testing.T) {
	bridge := &notificationBridge{wake: make(chan struct{}, 1)}
	conversation := newDaemonConversation(bridge)
	conversation.markInitialized()
	conversation.markReady(linkState{token: "initial", generation: 0})
	conversation.restore(linkState{token: "newest", generation: 2})
	conversation.restore(linkState{token: "stale", generation: 1})

	got, armed := bridge.snapshotDesired()
	if !armed || got.token != "newest" || got.generation != 2 {
		t.Fatalf("desired = %+v, armed = %v", got, armed)
	}
}

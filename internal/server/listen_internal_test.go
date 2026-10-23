//go:build test

package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestWaitOnListenMarksClientReadyOnlyForToolChanges(t *testing.T) {
	for _, wantsTools := range []bool{false, true} {
		session := newSession(clock.NewFake())
		session.markInitialized()
		var wg sync.WaitGroup
		ctx, cancel := context.WithCancel(context.Background())
		(&Server{}).waitOnListen(ctx, handleScannedLineParams{session: session, writeOut: func(any) {}, wg: &wg},
			listenSubscription{
				id:            json.RawMessage(`1`),
				notifications: transport.ListenNotifications{ToolsListChanged: wantsTools},
			})
		if got := session.clientReady.Load(); got != wantsTools {
			t.Errorf("listen for tool changes=%v left clientReady=%v", wantsTools, got)
		}
		cancel()
		wg.Wait()
	}
}

func TestWaitOnListenForgetsTheListenWhenItEnds(t *testing.T) {
	session := newSession(clock.NewFake())
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	(&Server{}).waitOnListen(
		ctx,
		handleScannedLineParams{session: session, writeOut: func(any) {}, wg: &wg},
		listenSubscription{
			id:            json.RawMessage(`"l"`),
			notifications: transport.ListenNotifications{ToolsListChanged: true},
		},
	)
	if session.listenTools[`"l"`] != true || session.inFlight[`"l"`] == nil {
		t.Fatal("listen was not registered for tool changes and cancellation")
	}
	cancel()
	wg.Wait()
	if len(session.listens) != 0 || len(session.listenTools) != 0 || len(session.inFlight) != 0 {
		t.Fatalf("ended listen left state: listens=%v tools=%v inFlight=%d",
			session.listens, session.listenTools, len(session.inFlight))
	}
}

func TestCancelListensEndsEveryListen(t *testing.T) {
	session := newSession(clock.NewFake())
	var wg sync.WaitGroup
	for _, id := range []string{`1`, `2`} {
		(&Server{}).waitOnListen(context.Background(),
			handleScannedLineParams{session: session, writeOut: func(any) {}, wg: &wg},
			listenSubscription{id: json.RawMessage(id)})
	}
	session.cancelListens()
	ended := make(chan struct{})
	go func() { wg.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelListens left a listen waiting")
	}
	if len(session.listens) != 0 || len(session.inFlight) != 0 {
		t.Fatalf("listens after cancelListens: %v, inFlight %d", session.listens, len(session.inFlight))
	}
}

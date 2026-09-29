//go:build test

package server

import (
	"context"
	"testing"
	"time"
)

func startBlockingConnect(b *pendingConnects, ctx context.Context) <-chan struct{} {
	started := make(chan struct{})
	b.start(ctx, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	})
	return started
}

func awaitConnectStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("connect never started")
	}
}

func waitWithin(t *testing.T, b *pendingConnects) {
	t.Helper()
	done := make(chan struct{})
	go func() { b.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("connects still running")
	}
}

func TestPendingConnects_stop_cancelsRunningConnects(t *testing.T) {
	b := newPendingConnects()
	awaitConnectStarted(t, startBlockingConnect(b, context.Background()))

	b.stop()

	waitWithin(t, b)
}

func TestPendingConnects_callerCancel_stopsItsConnect(t *testing.T) {
	b := newPendingConnects()
	ctx, cancel := context.WithCancel(context.Background())
	awaitConnectStarted(t, startBlockingConnect(b, ctx))

	cancel()

	waitWithin(t, b)
}

func TestPendingConnects_startAfterStop_neverRuns(t *testing.T) {
	b := newPendingConnects()
	b.stop()
	ran := false

	b.start(context.Background(), func(context.Context) { ran = true })
	b.wait()

	if ran {
		t.Error("a connect started after stop")
	}
}

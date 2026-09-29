//go:build test

package server

import (
	"context"
	"testing"
	"time"
)

func startBlockingConnect(p *pendingConnects, ctx context.Context) <-chan struct{} {
	started := make(chan struct{})
	p.start(ctx, func(ctx context.Context) {
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

func waitWithin(t *testing.T, p *pendingConnects) {
	t.Helper()
	done := make(chan struct{})
	go func() { p.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("connects still running")
	}
}

func TestPendingConnects_stop_cancelsRunningConnects(t *testing.T) {
	p := newPendingConnects()
	awaitConnectStarted(t, startBlockingConnect(p, context.Background()))

	p.stop()

	waitWithin(t, p)
}

func TestPendingConnects_callerCancel_stopsItsConnect(t *testing.T) {
	p := newPendingConnects()
	ctx, cancel := context.WithCancel(context.Background())
	awaitConnectStarted(t, startBlockingConnect(p, ctx))

	cancel()

	waitWithin(t, p)
}

func TestPendingConnects_startAfterStop_neverRuns(t *testing.T) {
	p := newPendingConnects()
	p.stop()
	ran := false

	p.start(context.Background(), func(context.Context) { ran = true })
	p.wait()

	if ran {
		t.Error("a connect started after stop")
	}
}

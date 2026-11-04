//go:build test

package server

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

type blockingConnects struct {
	started chan struct{}
}

func newBlockingConnector() (*upstreamConnector, blockingConnects) {
	b := blockingConnects{started: make(chan struct{}, 1)}
	c := newUpstreamConnector(func(ctx context.Context, _ upstreamInstall) {
		b.started <- struct{}{}
		<-ctx.Done()
	})
	return c, b
}

func (b blockingConnects) awaitStarted(t *testing.T) {
	t.Helper()
	testutil.WaitForReceive(t, b.started, "a connect to start")
}

func waitWithin(t *testing.T, c *upstreamConnector) {
	t.Helper()
	done := make(chan struct{})
	go func() { c.wait(); close(done) }()
	testutil.WaitForReceive(t, done, "the running connects to finish")
}

func TestUpstreamConnector_stop_cancelsRunningConnects(t *testing.T) {
	c, connects := newBlockingConnector()
	c.connect(context.Background(), upstreamInstall{})
	connects.awaitStarted(t)

	c.stop()

	waitWithin(t, c)
}

func TestUpstreamConnector_callerCancel_stopsItsConnect(t *testing.T) {
	c, connects := newBlockingConnector()
	ctx, cancel := context.WithCancel(context.Background())
	c.connect(ctx, upstreamInstall{})
	connects.awaitStarted(t)

	cancel()

	waitWithin(t, c)
}

func TestUpstreamConnector_connectAfterStop_neverRuns(t *testing.T) {
	c, connects := newBlockingConnector()
	c.stop()

	c.connect(context.Background(), upstreamInstall{})
	c.wait()

	select {
	case <-connects.started:
		t.Error("a connect started after stop")
	default:
	}
}

package server

import (
	"context"
	"sync"
)

type upstreamConnector struct {
	connectUntilRegistered func(context.Context, upstreamInstall)
	mu                     sync.Mutex
	stopped                bool
	lifetime               context.Context
	cancelAll              context.CancelFunc
	wg                     sync.WaitGroup
}

func newUpstreamConnector(connectUntilRegistered func(context.Context, upstreamInstall)) *upstreamConnector {
	lifetime, cancel := context.WithCancel(context.Background())
	return &upstreamConnector{connectUntilRegistered: connectUntilRegistered, lifetime: lifetime, cancelAll: cancel}
}

func (c *upstreamConnector) connect(ctx context.Context, in upstreamInstall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	//nolint:contextcheck // Caller cancellation is inherited above; connector lifetime adds owner cancellation.
	stopOnClose := context.AfterFunc(c.lifetime, cancel)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer stopOnClose()
		defer cancel()
		c.connectUntilRegistered(ctx, in)
	}()
}

func (c *upstreamConnector) stop() {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.cancelAll()
}

func (c *upstreamConnector) wait() {
	c.wg.Wait()
}

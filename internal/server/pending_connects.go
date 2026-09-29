package server

import (
	"context"
	"sync"
)

type pendingConnects struct {
	mu        sync.Mutex
	stopped   bool
	lifetime  context.Context
	cancelAll context.CancelFunc
	wg        sync.WaitGroup
}

func newPendingConnects() *pendingConnects {
	lifetime, cancel := context.WithCancel(context.Background())
	return &pendingConnects{lifetime: lifetime, cancelAll: cancel}
}

func (b *pendingConnects) start(ctx context.Context, connect func(context.Context)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	stopOnClose := context.AfterFunc(b.lifetime, cancel)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer stopOnClose()
		defer cancel()
		connect(ctx)
	}()
}

func (b *pendingConnects) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.cancelAll()
}

func (b *pendingConnects) wait() {
	b.wg.Wait()
}

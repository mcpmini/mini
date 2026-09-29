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

func (p *pendingConnects) start(ctx context.Context, connect func(context.Context)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	stopOnClose := context.AfterFunc(p.lifetime, cancel)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer stopOnClose()
		defer cancel()
		connect(ctx)
	}()
}

func (p *pendingConnects) stop() {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.cancelAll()
}

func (p *pendingConnects) wait() {
	p.wg.Wait()
}

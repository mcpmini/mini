package server

import (
	"context"
	"sync"
)

// backgroundConnects runs upstream connects that keep retrying after their
// caller returns. Close must cancel them all and wait for them, and none may
// start once Close has begun waiting.
type backgroundConnects struct {
	mu        sync.Mutex
	stopped   bool
	lifetime  context.Context
	cancelAll context.CancelFunc
	wg        sync.WaitGroup
}

func newBackgroundConnects() *backgroundConnects {
	lifetime, cancel := context.WithCancel(context.Background())
	return &backgroundConnects{lifetime: lifetime, cancelAll: cancel}
}

func (b *backgroundConnects) start(ctx context.Context, connect func(context.Context)) {
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

func (b *backgroundConnects) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.cancelAll()
}

func (b *backgroundConnects) wait() {
	b.wg.Wait()
}

package server

import (
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

var noRefreshToAwait = closedChannel()

func closedChannel() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

type toolListRefresher struct {
	startWorker func()

	mu        sync.Mutex
	stopped   bool
	running   bool
	pending   bool
	freshness transport.Freshness
	done      chan struct{}
}

func (r *toolListRefresher) request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.freshness = r.freshness.Expire()
	r.pending = true
	r.startLocked()
}

func (r *toolListRefresher) refreshIfStale(now time.Time) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return noRefreshToAwait
	}
	if !r.running {
		if !r.freshness.Stale(now) {
			return noRefreshToAwait
		}
		r.pending = true
		r.startLocked()
	}
	return r.done
}

func (r *toolListRefresher) startLocked() {
	if r.running {
		return
	}
	r.running = true
	r.done = make(chan struct{})
	// Under the lock, so a worker is registered before stop returns and shutdown can wait for it.
	r.startWorker()
}

// A list fetched by a pass that a notification overtook may predate the change, so it stays stale
// until the follow-up pass succeeds.
func (r *toolListRefresher) published(freshness transport.Freshness) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pending {
		r.freshness = freshness
	}
}

func (r *toolListRefresher) next() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pending || r.stopped {
		r.running = false
		close(r.done)
		return false
	}
	r.pending = false
	return true
}

func (r *toolListRefresher) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
}

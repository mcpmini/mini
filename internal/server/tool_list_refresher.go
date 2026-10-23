package server

import "sync"

type toolListRefresher struct {
	startWorker func()

	mu      sync.Mutex
	stopped bool
	running bool
	pending bool
}

func (r *toolListRefresher) request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.pending = true
	if r.running {
		return
	}
	r.running = true
	// Under the lock, so a worker is registered before stop returns and shutdown can wait for it.
	r.startWorker()
}

func (r *toolListRefresher) next() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pending || r.stopped {
		r.running = false
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

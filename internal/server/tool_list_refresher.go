package server

import "sync"

// toolListRefresher coalesces tool-list refreshes for one upstream: at most one worker runs, and
// requests that arrive while it runs collapse into one more pass.
type toolListRefresher struct {
	mu      sync.Mutex
	stopped bool
	running bool
	pending bool
}

// request runs startWorker only when no worker is running. startWorker is called under the
// refresher's lock, so a worker can never be registered after stop returns.
func (r *toolListRefresher) request(startWorker func()) {
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
	startWorker()
}

// next reports whether the worker should run another pass; false ends the worker.
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

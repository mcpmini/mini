package server

import "sync"

// goroutineGroup starts an incarnation's goroutines and lets stop wait for all of them. Its lock is
// a leaf, so callers may start goroutines while holding the refresher's or the incarnation's lock.
type goroutineGroup struct {
	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

// Go starts f unless the group is stopped. Add happens under the lock that markStopped takes, so
// no Add can race the Wait that follows a stop.
func (g *goroutineGroup) Go(f func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return false
	}
	g.wg.Go(f)
	return true
}

func (g *goroutineGroup) Stopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}

func (g *goroutineGroup) markStopped() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
}

func (g *goroutineGroup) Wait() { g.wg.Wait() }

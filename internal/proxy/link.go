package proxy

import (
	"sync"
	"sync/atomic"
)

type linkState struct {
	token      string
	generation uint64
}

type daemonLink struct {
	mu         sync.Mutex
	state      atomic.Pointer[linkState]
	resolveErr error // set when Resolve() fails; cleared on next successful resolve
}

func newDaemonLink(token string) *daemonLink {
	d := &daemonLink{}
	d.state.Store(&linkState{token: token})
	return d
}

func (d *daemonLink) snapshot() linkState {
	// no d.mu: a request that starts during Resolve must keep its old generation so recover won't resolve again
	return *d.state.Load()
}

func (d *daemonLink) recover(failedGen uint64, resolver *DaemonResolver) (linkState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.snapshot()
	if current.generation != failedGen || resolver == nil {
		// a concurrent caller already recovered, or self-healing is off;
		// propagate any resolve error so callers fail fast instead of retrying
		return current, d.resolveErr
	}
	// Many callers can hit a dead daemon at once; holding the lock across
	// Resolve means the first one respawns and bumps the generation
	// while the rest fall out at the guard above.
	d.resolveErr = nil
	t, err := resolver.Resolve()
	if err != nil {
		// bump so callers holding the failed generation skip Resolve and fail fast
		next := linkState{token: current.token, generation: current.generation + 1}
		d.state.Store(&next)
		d.resolveErr = err
		return next, err
	}
	next := linkState{token: t, generation: current.generation + 1}
	d.state.Store(&next)
	return next, nil
}

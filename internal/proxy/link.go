package proxy

import "sync"

type linkState struct {
	token      string
	generation uint64
}

type daemonLink struct {
	mu         sync.Mutex
	state      linkState
	resolveErr error // set when Resolve() fails; cleared on next successful resolve
}

func newDaemonLink(token string) *daemonLink {
	return &daemonLink{state: linkState{token: token}}
}

func (d *daemonLink) snapshot() linkState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *daemonLink) recover(failedGen uint64, resolver *DaemonResolver) (linkState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.generation != failedGen || resolver == nil {
		// a concurrent caller already recovered, or self-healing is off;
		// propagate any resolve error so callers fail fast instead of retrying
		return d.state, d.resolveErr
	}
	// Many callers can hit a dead daemon at once; holding the lock across
	// Resolve means the first one respawns and bumps the generation
	// while the rest fall out at the guard above.
	d.resolveErr = nil
	t, err := resolver.Resolve()
	if err != nil {
		d.state.generation++ // bump so subsequent callers skip Resolve and fail fast
		d.resolveErr = err
		return d.state, err
	}
	d.state = linkState{token: t, generation: d.state.generation + 1}
	return d.state, nil
}

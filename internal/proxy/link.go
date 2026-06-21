package proxy

import "sync"

type linkState struct {
	token      string
	generation uint64
}

type daemonLink struct {
	mu    sync.Mutex
	state linkState
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
		// a concurrent caller already recovered, or self-healing is off
		return d.state, nil
	}
	// Many callers can hit a dead daemon at once; holding the lock across
	// Resolve means the first one respawns and bumps the generation
	// while the rest fall out at the guard above.
	t, err := resolver.Resolve()
	if err != nil {
		return d.state, err
	}
	d.state = linkState{token: t, generation: d.state.generation + 1}
	return d.state, nil
}

package proxy

import "sync"

// gen tracks the daemon generation so concurrent forwards can collapse a failure into a single recovery.
type linkState struct {
	token string
	gen   uint64
}

// The first goroutine to detect a dead daemon re-resolves and bumps gen; the rest see the new gen and reuse the result.
type daemonLink struct {
	mu        sync.Mutex
	state     linkState
	reresolve func() (token string, err error) // nil = recovery disabled (standalone)
}

func newDaemonLink(token string, reresolve func() (string, error)) *daemonLink {
	return &daemonLink{state: linkState{token: token}, reresolve: reresolve}
}

func (d *daemonLink) snapshot() linkState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// At most one re-resolution per failed generation; lock intentionally held across reresolve()
// (which may spawn a daemon), serializing racers so exactly one respawns.
func (d *daemonLink) recover(failedGen uint64) (linkState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.gen != failedGen || d.reresolve == nil {
		return d.state, nil
	}
	t, err := d.reresolve()
	if err != nil {
		return d.state, err
	}
	d.state = linkState{token: t, gen: d.state.gen + 1}
	return d.state, nil
}

package proxy

import "sync"

// linkState is an immutable snapshot of the daemon target: where to reach it, the
// bearer token to use, and the generation it belongs to. gen lets concurrent forwards
// collapse a daemon failure into a single recovery (single-flight).
type linkState struct {
	port  int
	token string
	gen   uint64
}

// daemonLink is the shared, mutex-guarded target all concurrent forwards point at.
// The first forward to observe a dead daemon re-resolves and bumps gen; the rest see
// the advanced gen and reuse the fresh port+token without re-resolving.
type daemonLink struct {
	mu        sync.Mutex
	state     linkState
	reresolve func() (int, string, error) // nil = recovery disabled (standalone)
}

func newDaemonLink(port int, token string, reresolve func() (int, string, error)) *daemonLink {
	return &daemonLink{state: linkState{port: port, token: token}, reresolve: reresolve}
}

func (d *daemonLink) snapshot() linkState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// recover performs at most one re-resolution per failed generation. If another goroutine
// already advanced the generation, it returns the new state without re-resolving. The
// lock is intentionally held across reresolve() (which may spawn a daemon): serializing
// racers is what guarantees only one of them respawns.
func (d *daemonLink) recover(failedGen uint64) (linkState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.gen != failedGen || d.reresolve == nil {
		return d.state, nil
	}
	p, t, err := d.reresolve()
	if err != nil {
		return d.state, err
	}
	d.state = linkState{port: p, token: t, gen: d.state.gen + 1}
	return d.state, nil
}

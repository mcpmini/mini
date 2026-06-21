package proxy

// DaemonResolver produces a live daemon and its auth token.
type DaemonResolver struct {
	fn func() (string, error)
}

// NewDaemonResolver wraps fn as a DaemonResolver. Pass nil to Resolver to disable self-healing.
func NewDaemonResolver(fn func() (string, error)) *DaemonResolver {
	return &DaemonResolver{fn: fn}
}

func (r *DaemonResolver) Resolve() (string, error) {
	return r.fn()
}

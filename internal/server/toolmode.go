package server

// ToolMode selects how a session exposes upstream tools. Passthrough is the
// zero value, so an unconfigured session (including every daemon session) is
// passthrough for free — compact is the only mode anyone signals.
type ToolMode int32

const (
	// ToolModePassthrough exposes upstream tools directly as server__tool and
	// minifies responses; mini is transparent in the middle.
	ToolModePassthrough ToolMode = iota
	// ToolModeCompact wraps all upstreams behind four meta-tools
	// (list/call/perm_call/config).
	ToolModeCompact
)

func (m ToolMode) String() string {
	if m == ToolModeCompact {
		return "compact"
	}
	return "passthrough"
}

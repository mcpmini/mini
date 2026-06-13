package proxy

import (
	"encoding/json"
	"time"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

const (
	maxRecoveryAttempts = 3
	recoveryBackoff     = 50 * time.Millisecond
)

// deliver forwards one request, recovering transparently from a dead daemon, a rotated
// token, or a lost session. Recovery is bounded; a non-idempotent failure (outcomeOther)
// is never retried. Returns the bytes to write back, or nil for notifications.
func (p forwardAsyncParams) deliver() []byte {
	state := p.link.snapshot()
	isInit := peekIsInitialize(p.line)
	for attempt := 0; attempt < maxRecoveryAttempts; attempt++ {
		out := classifyForward(p.connAt(state), p.line)
		if out.kind == outcomeOK || out.kind == outcomeOther {
			return out.resp
		}
		next, ok := p.handleRecoverable(out.kind, state, isInit)
		if !ok {
			return out.resp
		}
		state = next
		time.Sleep(jitteredBackoff(attempt))
	}
	return daemonErrorResponse(p.line, "daemon unreachable: recovery exhausted")
}

// handleRecoverable re-resolves the daemon (for transport/auth failures) or reuses the
// current target (for a lost session), then reinitializes the session so the retry lands
// on a ready daemon. ok=false means recovery is not possible; the caller returns out.resp.
func (p forwardAsyncParams) handleRecoverable(kind outcomeKind, state linkState, isInit bool) (linkState, bool) {
	if kind == outcomeNotInitialized && isInit {
		return state, false
	}
	if kind == outcomeTransportDown || kind == outcomeUnauthorized {
		next, err := p.link.recover(state.gen)
		if err != nil || (next.gen == state.gen && p.link.reresolve == nil) {
			return state, false
		}
		state = next
	}
	if !isInit {
		reinitDaemon(p.connAt(state), p.proxyMode)
	}
	return state, true
}

func jitteredBackoff(attempt int) time.Duration {
	base := recoveryBackoff << attempt
	return base + time.Duration(int64(attempt)*int64(recoveryBackoff)/2)
}

// reinitDaemon recovers a session on a respawned or session-less daemon. Responses are
// discarded — only the caller's retry of the original request is forwarded.
func reinitDaemon(conn daemonConn, proxyMode bool) {
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: version.Version},
	})
	initMsg, _ := json.Marshal(transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)})
	forward(conn, maybeInjectProxy(initMsg, proxyMode))
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	forward(conn, notif)
}

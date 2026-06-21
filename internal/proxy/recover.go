package proxy

import (
	"encoding/json"
	"math/rand/v2"
	"time"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

const (
	maxRecoveryAttempts = 3
	recoveryBackoff     = 50 * time.Millisecond
)

// deliver transparently recovers from a dead daemon, rotated token, or lost session.
// Bounded retry; outcomeOther is never retried (non-idempotent write may have run).
func (p forwardAsyncParams) deliver() []byte {
	state := p.link.snapshot()
	isInit := peekIsInitialize(p.line)
	var out forwardOutcome
	for attempt := range maxRecoveryAttempts {
		out = classifyForward(p.connAt(state), p.line)
		if out.kind == outcomeOK || out.kind == outcomeOther {
			break
		}
		next, ok := p.handleRecoverable(out.kind, state, isInit)
		if !ok || attempt+1 == maxRecoveryAttempts {
			break
		}
		state = next
		time.Sleep(jitteredBackoff(attempt))
	}
	return out.resp
}

func (p forwardAsyncParams) handleRecoverable(kind outcomeKind, state linkState, isInit bool) (linkState, bool) {
	if kind == outcomeNotInitialized && isInit {
		return state, false
	}
	if kind == outcomeTransportDown || kind == outcomeUnauthorized {
		next, err := p.link.recover(state.generation)
		if err != nil || next.generation == state.generation {
			return state, false
		}
		state = next
	}
	// Every goroutine with a new gen reinits independently; concurrent reinit on a session is idempotent.
	if !isInit {
		reinitDaemon(p.connAt(state), p.toolMode)
	}
	return state, true
}

func jitteredBackoff(attempt int) time.Duration {
	base := recoveryBackoff << attempt
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}

// Responses are discarded — the caller retries the original request after reinit completes.
func reinitDaemon(conn daemonConn, mode transport.ToolMode) {
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: version.Version},
	})
	initMsg, _ := json.Marshal(transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)})
	forward(conn, maybeInjectToolMode(initMsg, mode))
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	forward(conn, notif)
}

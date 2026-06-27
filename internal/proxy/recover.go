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

// Forward transparently recovers from a dead daemon, rotated token, or lost session.
// Non-idempotent responses (outcomeOther) are never retried.
func (f *Forwarder) Forward(line []byte) []byte {
	state := f.link.snapshot()
	isInit := peekIsInitialize(line)
	var out forwardOutcome
	for attempt := range maxRecoveryAttempts {
		out = classifyForward(f.sessionAt(state), line)
		if out.kind == outcomeOK || out.kind == outcomeOther {
			break
		}
		next, ok := f.handleRecoverable(out.kind, state, isInit)
		if !ok || attempt+1 == maxRecoveryAttempts {
			break
		}
		state = next
		<-f.clock.NewTimer(jitteredBackoff(attempt)).Chan()
	}
	return out.resp
}

func (f *Forwarder) handleRecoverable(kind outcomeKind, state linkState, isInit bool) (linkState, bool) {
	if kind == outcomeNotInitialized && isInit {
		return state, false
	}
	if kind == outcomeTransportDown || kind == outcomeUnauthorized {
		next, err := f.link.recover(state.generation, f.resolver)
		if err != nil || next.generation == state.generation {
			return state, false
		}
		state = next
	}
	// reinit is idempotent — the daemon silently accepts duplicate initialize calls, so every goroutine can replay it safely
	if !isInit {
		f.sessionAt(state).Handshake(f.toolMode)
	}
	return state, true
}

func jitteredBackoff(attempt int) time.Duration {
	base := recoveryBackoff << attempt
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}

// Responses are discarded; the caller retries the original request after handshake completes.
func (s DaemonSession) Handshake(mode transport.ToolMode) {
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: version.Version},
	})
	initMsg, _ := json.Marshal(transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)})
	s.Send(maybeInjectToolMode(initMsg, mode))
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	s.Send(notif)
}

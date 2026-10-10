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

func jitteredBackoff(attempt int) time.Duration {
	base := recoveryBackoff << attempt
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}

// Responses are discarded; the caller retries the original request after handshake completes.
func (s DaemonSession) Handshake(mode transport.ToolMode) {
	//nolint:errcheck // fixed handshake fields contain only JSON-serializable values.
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: version.Version},
	})
	//nolint:errcheck // fixed request fields contain the serialized handshake params.
	initMsg, _ := json.Marshal(
		transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)},
	)
	s.Send(maybeInjectToolMode(initMsg, mode))
	//nolint:errcheck // fixed notification fields are JSON-serializable.
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	s.Send(notif)
}

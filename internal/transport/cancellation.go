package transport

import (
	"encoding/json"
	"time"
)

// A best-effort notice for a caller that has already given up, so it gets only a short bound.
const upstreamCancelTimeout = 250 * time.Millisecond

// initialize MUST NOT be cancelled, and server/discover is sent before the era, and so the
// cancellation mechanism, is known:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2025-11-25/basic/utilities/cancellation.mdx#L35
func methodAcceptsCancellation(method string) bool {
	return method != "initialize" && method != "server/discover"
}

func cancelledNotification(requestID json.RawMessage) (Notification, error) {
	params, err := json.Marshal(CancelledParams{RequestID: requestID})
	if err != nil {
		return Notification{}, err
	}
	return Notification{JsonRPC: JsonRPCVersion, Method: NotificationCancelled, Params: params}, nil
}

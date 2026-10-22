package transport

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

const MethodPing = "ping"

// Bounds a reply to a server that has stopped reading stdin; nothing in mini waits on it.
const stdioReplyWriteTimeout = 5 * time.Second

// Legacy servers may ping mini and drop a connection that never answers. mini declares no client
// capabilities, so every other server request is refused rather than left waiting.
func (c *StdioConnection) answerServerRequest(ctx context.Context, id any, method string) {
	reply := Response{JsonRPC: JsonRPCVersion, ID: id, Result: []byte(`{}`)}
	if method != MethodPing || c.isModern() {
		reply.Result = nil
		reply.Error = &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + method}
	}
	// Not on the reader: a server blocked writing stdout would never read this reply off stdin.
	go c.sendReply(ctx, reply)
}

func (c *StdioConnection) sendReply(parent context.Context, reply Response) {
	ctx, cancel := clock.WithTimeout(parent, c.clock, stdioReplyWriteTimeout)
	defer cancel()
	if err := c.writeJSONContext(ctx, reply); err != nil {
		c.logger.Debug("reply to upstream request", "err", err)
		c.abandonAfterFailedWrite(ctx, err)
	}
}

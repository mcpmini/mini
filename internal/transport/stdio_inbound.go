package transport

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func (c *StdioConnection) readLoop(ctx context.Context) {
	defer c.closeDone()
	for c.scanner.Scan() {
		c.handleMessage(ctx, c.scanner.Bytes())
	}
	if err := c.scanner.Err(); err != nil {
		c.logger.Error("upstream read error", "err", err)
	}
}

func (c *StdioConnection) handleMessage(ctx context.Context, line []byte) {
	var message struct {
		Response
		Method string `json:"method"`
	}
	if err := json.Unmarshal(line, &message); err != nil {
		c.logger.Error("bad upstream message", "err", err)
		return
	}
	resp := &message.Response
	switch {
	case resp.ID == nil:
		c.handleNotification(line)
	case message.Method != "":
		c.answerServerRequest(ctx, resp.ID, message.Method)
	default:
		c.noteSubscriptionResponse(resp)
		c.pending.deliver(resp.ID, resp)
	}
}

const MethodPing = "ping"

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

func (c *StdioConnection) handleNotification(line []byte) {
	var notification Notification
	if err := json.Unmarshal(line, &notification); err != nil || notification.Method == "" {
		return
	}
	if c.isModern() && c.dispatchModernSubscriptionNotification(notification) {
		return
	}
	if notification.Method == NotificationToolsChanged {
		c.toolsChanged.NotifyToolsChanged()
		return
	}
	if handler := c.toolsChanged.Handler(); handler != nil {
		handler(notification)
	}
}

func (c *StdioConnection) SetNotificationHandler(handler func(Notification)) {
	c.toolsChanged.SetHandler(handler)
}

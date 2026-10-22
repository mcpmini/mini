package transport

import (
	"context"
	"encoding/json"
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
		c.logger.Error("bad upstream response", "err", err)
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

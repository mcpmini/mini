package transport

import "encoding/json"

func (c *StdioConnection) readLoop() {
	defer c.closeDone()
	for c.scanner.Scan() {
		c.handleMessage(c.scanner.Bytes())
	}
	if err := c.scanner.Err(); err != nil {
		c.logger.Error("upstream read error", "err", err)
	}
}

func (c *StdioConnection) handleMessage(line []byte) {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		c.logger.Error("bad upstream response", "err", err)
		return
	}
	if resp.ID == nil {
		c.handleNotification(line)
		return
	}
	c.noteSubscriptionResponse(&resp)
	c.pending.deliver(resp.ID, &resp)
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

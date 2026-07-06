package proxy

import "sync/atomic"

type daemonConversation struct {
	bridge      *notificationBridge
	initialized atomic.Bool
	ready       atomic.Bool
}

func newDaemonConversation(bridge *notificationBridge) *daemonConversation {
	return &daemonConversation{bridge: bridge}
}

func (c *daemonConversation) markInitialized() {
	c.initialized.Store(true)
}

func (c *daemonConversation) markReady(state linkState) {
	if !c.initialized.Load() || c.bridge == nil {
		return
	}
	c.ready.Store(true)
	c.bridge.Arm(state)
}

func (c *daemonConversation) restore(state linkState) {
	c.markInitialized()
	if c.ready.Load() && c.bridge != nil {
		c.bridge.Arm(state)
	}
}

func (c *daemonConversation) close() {
	if c.bridge != nil {
		c.bridge.Close()
	}
}

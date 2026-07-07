package transport

import "sync"

type toolsChangedNotifier struct {
	mu      sync.Mutex
	handler func(Notification)
	pending bool
}

func (n *toolsChangedNotifier) SetHandler(handler func(Notification)) {
	n.mu.Lock()
	n.handler = handler
	pending := n.pending && handler != nil
	n.pending = false
	n.mu.Unlock()
	if pending {
		handler(Notification{JSONRPC: "2.0", Method: NotificationToolsChanged})
	}
}

func (n *toolsChangedNotifier) NotifyToolsChanged() {
	n.mu.Lock()
	handler := n.handler
	if handler == nil {
		n.pending = true
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()
	handler(Notification{JSONRPC: "2.0", Method: NotificationToolsChanged})
}

func (n *toolsChangedNotifier) Handler() func(Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.handler
}

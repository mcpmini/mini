package transport

import "sync"

type toolsChangedLatch struct {
	mu      sync.Mutex
	handler func(Notification)
	pending bool
}

func (l *toolsChangedLatch) Dispatch(notification Notification) {
	l.mu.Lock()
	handler := l.handler
	if handler == nil {
		if notification.Method == NotificationToolsChanged {
			l.pending = true
		}
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()
	handler(notification)
}

func (l *toolsChangedLatch) SetHandler(handler func(Notification)) {
	l.mu.Lock()
	l.handler = handler
	pending := l.pending && handler != nil
	l.pending = false
	l.mu.Unlock()
	if pending {
		handler(Notification{JSONRPC: "2.0", Method: NotificationToolsChanged})
	}
}

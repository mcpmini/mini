package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

type sessionNotifications struct {
	mu      sync.Mutex
	streams map[uint64]chan json.RawMessage
	pending json.RawMessage
	nextID  uint64
	closed  bool
}

func newSessionNotifications() sessionNotifications {
	return sessionNotifications{streams: make(map[uint64]chan json.RawMessage)}
}

func (n *sessionNotifications) open() (chan json.RawMessage, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, false
	}
	ch := make(chan json.RawMessage, 16)
	n.nextID++
	n.streams[n.nextID] = ch
	if n.pending != nil {
		ch <- n.pending
		n.pending = nil
	}
	return ch, true
}

func (n *sessionNotifications) close(stream chan json.RawMessage) {
	n.mu.Lock()
	for id, current := range n.streams {
		if current != stream {
			continue
		}
		delete(n.streams, id)
		close(stream)
		break
	}
	n.mu.Unlock()
}

func (n *sessionNotifications) closeAll() {
	n.mu.Lock()
	n.closed = true
	n.pending = nil
	for id, stream := range n.streams {
		delete(n.streams, id)
		close(stream)
	}
	n.mu.Unlock()
}

func (n *sessionNotifications) hasOpenStreams() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.streams) > 0
}

func (n *sessionNotifications) publish(message json.RawMessage) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return false
	}
	if len(n.streams) == 0 {
		n.pending = bytes.Clone(message)
		return true
	}
	for _, stream := range n.streams {
		select {
		case stream <- message:
			return true
		default:
		}
	}
	return false
}

func (s *Session) enableNotifications() (chan json.RawMessage, bool) {
	return s.notifications.open()
}

func (s *Session) disableNotifications(ch chan json.RawMessage) {
	s.notifications.close(ch)
}

func (s *Session) closeNotificationStreams() {
	s.notifications.closeAll()
}

func (s *Session) notify(msg json.RawMessage) {
	if !s.clientReady.Load() || s.toolMode() != transport.ToolModeProxy {
		return
	}
	if s.notifications.publish(msg) {
		return
	}
	slog.Debug("notification queue full, dropping event")
}

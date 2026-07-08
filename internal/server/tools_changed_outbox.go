package server

import (
	"encoding/json"
	"log/slog"
	"sync"
)

type toolsChangedOutbox struct {
	mu      sync.Mutex
	streams map[uint64]chan json.RawMessage
	pending bool
	nextID  uint64
	closed  bool
}

func newToolsChangedOutbox() toolsChangedOutbox {
	return toolsChangedOutbox{streams: make(map[uint64]chan json.RawMessage)}
}

func (o *toolsChangedOutbox) openStream() (chan json.RawMessage, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, false
	}
	stream := make(chan json.RawMessage, 16)
	o.nextID++
	o.streams[o.nextID] = stream
	if o.pending {
		stream <- toolsChangedNotif
		o.pending = false
	}
	return stream, true
}

func (o *toolsChangedOutbox) closeStream(stream chan json.RawMessage) {
	o.mu.Lock()
	for id, current := range o.streams {
		if current != stream {
			continue
		}
		delete(o.streams, id)
		close(stream)
		break
	}
	o.mu.Unlock()
}

func (o *toolsChangedOutbox) close() {
	o.mu.Lock()
	o.closed = true
	o.pending = false
	for id, stream := range o.streams {
		delete(o.streams, id)
		close(stream)
	}
	o.mu.Unlock()
}

func (o *toolsChangedOutbox) hasOpenStream() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.streams) > 0
}

func (o *toolsChangedOutbox) publishToolsChanged() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	if len(o.streams) == 0 {
		o.pending = true
		return true
	}
	for _, stream := range o.streams {
		select {
		case stream <- toolsChangedNotif:
			return true
		default:
		}
	}
	o.pending = true
	return true
}

func (s *Session) openToolsChangedStream() (chan json.RawMessage, bool) {
	return s.toolsChanged.openStream()
}

func (s *Session) closeToolsChangedStream(ch chan json.RawMessage) {
	s.toolsChanged.closeStream(ch)
}

func (s *Session) closeToolsChangedStreams() {
	s.toolsChanged.close()
}

func (s *Session) notifyToolsChanged() {
	if !s.clientReady.Load() {
		return
	}
	if s.toolsChanged.publishToolsChanged() {
		return
	}
	slog.Debug("notification queue full, dropping event")
}

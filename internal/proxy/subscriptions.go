package proxy

import (
	"bytes"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/mcpmini/mini/internal/transport"
)

// subscriptions records whether the agent speaks 2026-07-28 and which listens it holds. The lock
// also orders each acknowledgement before any notification tagged with that listen's ID.
type subscriptions struct {
	modern atomic.Bool
	mu     sync.Mutex
	ids    map[string]json.RawMessage
}

func newSubscriptions() *subscriptions {
	return &subscriptions{ids: make(map[string]json.RawMessage)}
}

func (s *subscriptions) acknowledge(ack transport.SubscriptionAck, write func(transport.SubscriptionAck) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := write(ack); err != nil {
		return err
	}
	if ack.Params.Notifications.ToolsListChanged {
		id := ack.Params.Meta.SubscriptionID
		s.ids[string(id)] = bytes.Clone(id)
	}
	return nil
}

func (s *subscriptions) remove(id json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[string(id)]; !ok {
		return false
	}
	delete(s.ids, string(id))
	return true
}

func (s *subscriptions) forEach(fn func(id json.RawMessage) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.ids {
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

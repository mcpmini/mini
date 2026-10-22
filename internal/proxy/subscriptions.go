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

func (s *proxySession) handleLocalModernLine(message modernLine) (bool, error) {
	switch message.Method {
	case transport.MethodSubscriptionsListen:
		if message.JsonRPC != transport.JsonRPCVersion || !message.isModern() || len(message.ID) == 0 ||
			string(message.ID) == "null" ||
			!s.clientReady.Load() {
			return false, nil
		}
		var params transport.ListenParams
		if json.Unmarshal(message.Params, &params) != nil {
			return true, s.writeInvalidListenParams(message.ID)
		}
		return true, s.acknowledgeListen(message.ID, params)
	case transport.NotificationCancelled:
		var params transport.CancelledParams
		if json.Unmarshal(message.Params, &params) != nil {
			return false, nil //nolint:nilerr // Malformed cancellation stays on the daemon path.
		}
		if s.subs.remove(params.RequestID) {
			return true, nil
		}
	}
	return false, nil
}

func (s *proxySession) acknowledgeListen(id json.RawMessage, params transport.ListenParams) error {
	toolsChanged := params.Notifications.ToolsListChanged && s.notificationStream != nil
	ack := transport.NewSubscriptionAck(id, transport.ListenNotifications{ToolsListChanged: toolsChanged})
	return s.subs.acknowledge(ack, func(ack transport.SubscriptionAck) error {
		line, err := json.Marshal(ack)
		if err != nil {
			return err
		}
		return s.writeLine(line)
	})
}

func (s *proxySession) writeInvalidListenParams(id json.RawMessage) error {
	line, err := json.Marshal(transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Error: &transport.RPCError{
		Code: transport.CodeInvalidParams, Message: transport.InvalidListenParamsMessage,
	}})
	if err != nil {
		return err
	}
	return s.writeLine(line)
}

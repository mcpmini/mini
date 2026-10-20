package proxy

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/transport"
)

type modernLine struct {
	JsonRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (m modernLine) isModern() bool {
	version, _ := transport.ProtocolVersionFromParams(m.Params)
	return transport.IsModernVersion(version)
}

func (s *proxySession) ensureModernReady() {
	s.subs.modern.Store(true)
	if s.clientReady.Load() {
		return
	}
	initialize, initialized := handshakeLines(s.toolMode)
	// forward recovers a stale or restarted daemon, which must happen before a listen is acknowledged locally.
	s.forward(forwardedMessage{line: initialize, kind: forwardedMessageInitialize})
	s.forward(forwardedMessage{line: initialized, kind: forwardedMessageInitialized})
}

func (s *proxySession) handleModernInput(line []byte) (bool, error) {
	var message modernLine
	if json.Unmarshal(line, &message) != nil {
		return false, nil //nolint:nilerr // Malformed lines must reach the daemon for its parse error.
	}
	if message.isModern() {
		s.ensureModernReady()
	}
	return s.handleLocalModernLine(message)
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

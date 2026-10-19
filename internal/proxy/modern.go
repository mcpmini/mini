package proxy

import (
	"encoding/json"
	"slices"

	"github.com/mcpmini/mini/internal/transport"
)

type modernLine struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type cancelledRequestParams struct {
	RequestID json.RawMessage `json:"requestId"`
}

func isModernLine(line []byte) bool {
	var message modernLine
	if json.Unmarshal(line, &message) != nil {
		return false
	}
	version, modern := transport.ProtocolVersionFromParams(message.Params)
	return modern && slices.Contains(transport.ModernVersions, version)
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
	if isModernLine(line) {
		s.ensureModernReady()
	}
	return s.handleLocalModernLine(line)
}

func (s *proxySession) handleLocalModernLine(line []byte) (bool, error) {
	var message modernLine
	if json.Unmarshal(line, &message) != nil {
		return false, nil //nolint:nilerr // Malformed lines must reach the daemon for its parse error.
	}
	switch message.Method {
	case transport.MethodSubscriptionsListen:
		if message.JSONRPC != "2.0" || !isModernLine(line) || len(message.ID) == 0 || string(message.ID) == "null" ||
			!s.clientReady.Load() {
			return false, nil
		}
		var params transport.ListenParams
		if json.Unmarshal(message.Params, &params) != nil {
			return true, s.writeInvalidListenParams(message.ID)
		}
		return true, s.acknowledgeListen(message.ID, params)
	case transport.NotificationCancelled:
		var params cancelledRequestParams
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
	line, err := json.Marshal(transport.Response{JSONRPC: "2.0", ID: id, Error: &transport.RPCError{
		Code: transport.CodeInvalidParams, Message: transport.InvalidListenParamsMessage,
	}})
	if err != nil {
		return err
	}
	return s.writeLine(line)
}

func tagNotification(line, id json.RawMessage) ([]byte, error) {
	var notification struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &notification); err != nil {
		return nil, err
	}
	params, err := tagNotificationParams(notification.Params, id)
	if err != nil {
		return nil, err
	}
	notification.Params = params
	return json.Marshal(notification)
}

func tagNotificationParams(raw, id json.RawMessage) (json.RawMessage, error) {
	params, err := decodeNotificationParams(raw)
	if err != nil {
		return nil, err
	}
	meta, err := addSubscriptionID(params["_meta"], id)
	if err != nil {
		return nil, err
	}
	params["_meta"] = meta
	return json.Marshal(params)
}

func decodeNotificationParams(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var params map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	}
	if params == nil {
		params = make(map[string]json.RawMessage)
	}
	return params, nil
}

func addSubscriptionID(raw, id json.RawMessage) (json.RawMessage, error) {
	meta := make(map[string]json.RawMessage)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
	}
	if meta == nil {
		meta = make(map[string]json.RawMessage)
	}
	meta[transport.SubscriptionIDMeta] = id
	return json.Marshal(meta)
}

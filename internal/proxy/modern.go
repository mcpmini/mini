package proxy

import (
	"encoding/json"
	"slices"

	"github.com/mcpmini/mini/internal/transport"
)

const subscriptionIDMeta = "io.modelcontextprotocol/subscriptionId"

type modernLine struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type listenRequestParams struct {
	Notifications struct {
		ToolsListChanged bool `json:"toolsListChanged"`
	} `json:"notifications"`
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
	if s.modern.Swap(true) {
		return
	}
	s.daemonAt(s.link.snapshot()).Handshake(s.toolMode)
	s.initialized.Store(true)
	s.clientReady.Store(true)
	s.bindNotifications(s.link.snapshot())
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
	case "subscriptions/listen":
		if message.JSONRPC != "2.0" || !isModernLine(line) || len(message.ID) == 0 || string(message.ID) == "null" {
			return false, nil
		}
		var params listenRequestParams
		if json.Unmarshal(message.Params, &params) != nil {
			return false, nil //nolint:nilerr // Malformed listen params stay on the daemon path.
		}
		return true, s.acknowledgeListen(message.ID, params)
	case transport.NotificationCancelled:
		var params cancelledRequestParams
		if json.Unmarshal(message.Params, &params) != nil {
			return false, nil //nolint:nilerr // Malformed cancellation stays on the daemon path.
		}
		if s.removeListen(params.RequestID) {
			return true, nil
		}
	}
	return false, nil
}

func (s *proxySession) acknowledgeListen(id json.RawMessage, params listenRequestParams) error {
	toolsChanged := params.Notifications.ToolsListChanged && s.notificationStream != nil
	notifications := modernListenNotifications{ToolsListChanged: toolsChanged}
	if !toolsChanged {
		notifications = modernListenNotifications{}
	}
	return s.writeListenAcknowledgment(listenAckParams{
		id: id, notifications: notifications, register: toolsChanged,
	})
}

type listenAckParams struct {
	id            json.RawMessage
	notifications modernListenNotifications
	register      bool
}

func (s *proxySession) writeListenAcknowledgment(p listenAckParams) error {
	line, err := marshalListenAcknowledgment(p.id, p.notifications)
	if err != nil {
		return err
	}
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	if err := s.writeLine(line); err != nil {
		return err
	}
	if p.register {
		s.listens[string(p.id)] = append(json.RawMessage(nil), p.id...)
	}
	return nil
}

func marshalListenAcknowledgment(id json.RawMessage, notifications modernListenNotifications) ([]byte, error) {
	ack := modernListenAcknowledgment{
		JSONRPC: "2.0", Method: "notifications/subscriptions/acknowledged",
		Params: modernListenParams{
			Meta: map[string]json.RawMessage{subscriptionIDMeta: id}, Notifications: notifications,
		},
	}
	return json.Marshal(ack)
}

type modernListenAcknowledgment struct {
	JSONRPC string             `json:"jsonrpc"`
	Method  string             `json:"method"`
	Params  modernListenParams `json:"params"`
}

type modernListenParams struct {
	Meta          map[string]json.RawMessage `json:"_meta"`
	Notifications modernListenNotifications  `json:"notifications"`
}

type modernListenNotifications struct {
	ToolsListChanged bool `json:"toolsListChanged,omitempty"`
}

func (s *proxySession) removeListen(id json.RawMessage) bool {
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	if _, ok := s.listens[string(id)]; !ok {
		return false
	}
	delete(s.listens, string(id))
	return true
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
	meta[subscriptionIDMeta] = id
	return json.Marshal(meta)
}

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
	return transport.IsModernVersion(transport.ProtocolVersionFromParams(m.Params))
}

func (s *proxySession) ensureModernReady() {
	s.subs.modern.Store(true)
	if s.clientReady.Load() {
		return
	}
	initialize, initialized := handshakeLines()
	// forward recovers a stale or restarted daemon, which must happen before a listen is acknowledged locally.
	s.forward(forwardedMessage{line: initialize, kind: forwardedMessageInitialize})
	s.forward(forwardedMessage{line: initialized, kind: forwardedMessageInitialized})
}

func (s *proxySession) handleModernInput(line []byte) (bool, error) {
	var message modernLine
	if json.Unmarshal(line, &message) != nil {
		return false, nil //nolint:nilerr // Malformed lines must reach the daemon for its parse error.
	}
	if message.JsonRPC == transport.JsonRPCVersion && len(message.ID) != 0 && string(message.ID) != "null" {
		if err := transport.ValidateModernRequestMeta(message.Params); err != nil {
			return true, s.writeInvalidRequestMeta(message.ID, err.Error())
		}
	}
	if message.isModern() {
		s.ensureModernReady()
	}
	return s.handleLocalModernLine(message)
}

func (s *proxySession) writeInvalidRequestMeta(id json.RawMessage, message string) error {
	line, err := json.Marshal(transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Error: &transport.RPCError{
		Code: transport.CodeInvalidParams, Message: message,
	}})
	if err != nil {
		return err
	}
	return s.writeLine(line)
}

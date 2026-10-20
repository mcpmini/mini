package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

func (s *Session) acknowledgeListen(listen listenSubscription, cancel context.CancelFunc, writeOut func(any)) {
	// Held across the write: a tool change written after the acknowledgement must find this listen.
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	writeOut(transport.NewSubscriptionAck(listen.id, listen.notifications))
	s.listens[string(listen.id)] = cancel
	s.listenTools[string(listen.id)] = listen.notifications.ToolsListChanged
}

func (s *Session) removeListen(id json.RawMessage) {
	s.listensMu.Lock()
	delete(s.listens, string(id))
	delete(s.listenTools, string(id))
	s.listensMu.Unlock()
}

func (s *Session) writeTaggedToolsChanged(writeOut func(any)) {
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	for id, wantsToolChanges := range s.listenTools {
		if wantsToolChanges {
			writeOut(transport.NewToolsListChangedNotification(json.RawMessage(id)))
		}
	}
}

func (s *Session) cancelListens() {
	s.listensMu.Lock()
	for _, cancel := range s.listens {
		cancel()
	}
	s.listens = make(map[string]context.CancelFunc)
	s.listenTools = make(map[string]bool)
	s.listensMu.Unlock()
}

func (s *Server) handleModernLine(p handleScannedLineParams, line []byte) bool {
	if len(peekRequestID(line)) == 0 {
		return false
	}
	_, modern := modernRequestVersion(line)
	if modern {
		s.activateModern(p.session)
	}
	if modern && peekMethod(line) == transport.MethodSubscriptionsListen {
		s.startListen(p.ctx, p, line)
		return true
	}
	return false
}

func modernRequestVersion(line []byte) (string, bool) {
	var request struct {
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &request) != nil {
		return "", false
	}
	return transport.ProtocolVersionFromParams(request.Params)
}

func (s *Server) activateModern(session *Session) {
	if session.modern.Swap(true) || session.httpSession.Load() {
		return
	}
	session.setToolModeOnce(s.resolveToolMode(""))
	session.markInitialized()
	session.clientReady.Store(true)
}

func modernVersionError(req transport.Request, session *Session) *transport.Response {
	version, modern := transport.ProtocolVersionFromParams(req.Params)
	if !modern || slices.Contains(transport.ModernVersions, version) {
		return nil
	}
	response, err := unsupportedVersionResponse(req.ID, version)
	if err != nil {
		internal := errorResponse(req.ID, transport.CodeInternalError, "encode unsupported protocol version")
		return &internal
	}
	return &response
}

func unsupportedVersionResponse(id any, requested string) (transport.Response, error) {
	supported := append(append([]string{}, transport.ModernVersions...), transport.LegacyVersions...)
	data, err := json.Marshal(transport.UnsupportedVersionData{Supported: supported, Requested: requested})
	if err != nil {
		return transport.Response{}, err
	}
	return transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Error: &transport.RPCError{
		Code: transport.CodeUnsupportedProtocolVersion, Message: "Unsupported protocol version", Data: data,
	}}, nil
}

func addModernResultFields(method string, result any) (any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode modern result object: %w", err)
	}
	if fields == nil {
		return nil, fmt.Errorf("modern result must be a JSON object")
	}
	_, hasResultType := fields["resultType"]
	if hasResultType && method != "tools/list" {
		return result, nil // An upstream result that sets its own type reaches the agent byte-identical.
	}
	addMissingField(fields, "resultType", json.RawMessage(`"complete"`))
	if method == "tools/list" {
		addMissingField(fields, "ttlMs", json.RawMessage(`0`))
		addMissingField(fields, "cacheScope", json.RawMessage(`"private"`))
	}
	return fields, nil
}

func addMissingField(fields map[string]json.RawMessage, key string, value json.RawMessage) {
	if _, ok := fields[key]; !ok {
		fields[key] = value
	}
}

func (s *Server) handleDiscover(session *Session) transport.DiscoverResult {
	supported := append(append([]string{}, transport.ModernVersions...), transport.LegacyVersions...)
	return transport.DiscoverResult{
		ResultType:        string(transport.ResultComplete),
		SupportedVersions: supported,
		Capabilities:      transport.ServerCapabilities{Tools: &transport.ToolsCapability{ListChanged: true}},
		Instructions:      s.initInstructions(session),
		Meta: &transport.DiscoverMeta{
			ServerInfo: transport.ServerInfo{Name: "mini", Version: version.Version},
		},
		CacheScope: "private",
	}
}

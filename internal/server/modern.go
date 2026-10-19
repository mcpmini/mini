package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

type handleScannedLineParams struct {
	ctx      context.Context
	rawLine  []byte
	session  *Session
	writeOut func(any)
	wg       *sync.WaitGroup
}

func (s *Session) addListen(id json.RawMessage, cancel context.CancelFunc, tools bool) {
	s.listensMu.Lock()
	s.listens[string(id)] = cancel
	s.listenTools[string(id)] = tools
	s.listensMu.Unlock()
}

func (s *Session) removeListen(id json.RawMessage) {
	s.listensMu.Lock()
	delete(s.listens, string(id))
	delete(s.listenTools, string(id))
	s.listensMu.Unlock()
}

func (s *Session) listenIDs() []json.RawMessage {
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	ids := make([]json.RawMessage, 0, len(s.listens))
	for id, tools := range s.listenTools {
		if tools {
			ids = append(ids, json.RawMessage(id))
		}
	}
	return ids
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

// Raw IDs preserve JSON-RPC's distinction between numeric and string IDs.
func (s *Session) registerInFlight(id json.RawMessage, cancel context.CancelFunc) {
	s.inFlightMu.Lock()
	s.inFlight[string(id)] = cancel
	s.inFlightMu.Unlock()
}

func (s *Session) removeInFlight(id json.RawMessage) {
	s.inFlightMu.Lock()
	delete(s.inFlight, string(id))
	s.inFlightMu.Unlock()
}

func (s *Session) cancelInFlight(id json.RawMessage) {
	s.inFlightMu.Lock()
	cancel := s.inFlight[string(id)]
	s.inFlightMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) handleScannedLine(p handleScannedLineParams) {
	if len(p.rawLine) == 0 {
		return
	}
	line := bytes.Clone(p.rawLine)
	if s.handleModernLine(p, line) {
		return
	}
	method := peekMethod(line)
	// Lifecycle and cancellation transitions must finish before later requests start.
	if method == "initialize" || method == transport.NotificationInitialized ||
		method == transport.NotificationCancelled {
		if resp, send := s.handleLine(p.ctx, line, p.session); send {
			p.writeOut(resp)
		}
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		s.dispatchWithCancel(p.ctx, line, p.session, p.writeOut)
	}()
}

func (s *Server) handleModernLine(p handleScannedLineParams, line []byte) bool {
	if len(peekRequestID(line)) == 0 || !p.session.acceptsModern.Load() {
		return false
	}
	_, modern := modernRequestVersion(line)
	if modern {
		s.activateModern(p.session)
	}
	if modern && peekMethod(line) == "subscriptions/listen" {
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
	return protocolVersionFromParams(request.Params)
}

func protocolVersionFromParams(params json.RawMessage) (string, bool) {
	var request struct {
		Meta struct {
			ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &request) != nil || request.Meta.ProtocolVersion == "" {
		return "", false
	}
	return request.Meta.ProtocolVersion, true
}

func (s *Server) activateModern(session *Session) {
	if session.modern.Swap(true) {
		return
	}
	session.setToolModeOnce(s.resolveToolMode(""))
	session.markInitialized()
	session.clientReady.Store(true)
}

func modernVersionError(req transport.Request, session *Session) *transport.Response {
	version, modern := protocolVersionFromParams(req.Params)
	if !modern || !session.acceptsModern.Load() || slices.Contains(transport.ModernVersions, version) {
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
	return transport.Response{JSONRPC: "2.0", ID: id, Error: &transport.RPCError{
		Code: -32022, Message: "Unsupported protocol version", Data: data,
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
	if _, ok := fields["resultType"]; !ok {
		fields["resultType"] = json.RawMessage(`"complete"`)
	}
	if method == "tools/list" {
		if _, ok := fields["ttlMs"]; !ok {
			fields["ttlMs"] = json.RawMessage(`0`)
		}
		if _, ok := fields["cacheScope"]; !ok {
			fields["cacheScope"] = json.RawMessage(`"private"`)
		}
	}
	return fields, nil
}

type modernDiscoverResult struct {
	ResultType        string             `json:"resultType"`
	SupportedVersions []string           `json:"supportedVersions"`
	Capabilities      modernCapabilities `json:"capabilities"`
	Instructions      string             `json:"instructions"`
	Meta              modernDiscoverMeta `json:"_meta"`
	TTLMS             int                `json:"ttlMs"`
	CacheScope        string             `json:"cacheScope"`
}

type modernCapabilities struct {
	Tools modernToolsCapability `json:"tools"`
}

type modernToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type modernDiscoverMeta struct {
	ServerInfo transport.ServerInfo `json:"io.modelcontextprotocol/serverInfo"`
}

func (s *Server) handleDiscover(session *Session) modernDiscoverResult {
	supported := append(append([]string{}, transport.ModernVersions...), transport.LegacyVersions...)
	return modernDiscoverResult{
		ResultType:        "complete",
		SupportedVersions: supported,
		Capabilities:      modernCapabilities{Tools: modernToolsCapability{ListChanged: true}},
		Instructions:      s.initInstructions(session),
		Meta:              modernDiscoverMeta{ServerInfo: transport.ServerInfo{Name: "mini", Version: version.Version}},
		TTLMS:             0, CacheScope: "private",
	}
}

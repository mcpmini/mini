package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

func modernRequestVersion(line []byte) string {
	var request struct {
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &request) != nil {
		return ""
	}
	return transport.ProtocolVersionFromParams(request.Params)
}

func (s *Server) activateModern(session *Session) {
	if session.modern.Swap(true) || session.httpSession.Load() {
		return
	}
	session.markInitialized()
	session.clientReady.Store(true)
}

func modernVersionError(req transport.Request) *transport.Response {
	version := transport.ProtocolVersionFromParams(req.Params)
	if version == "" || transport.IsModernVersion(version) {
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
	data, err := json.Marshal(
		transport.UnsupportedVersionData{Supported: transport.SupportedVersions(), Requested: requested},
	)
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
	if err := addMiniServerIdentity(fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func addMiniServerIdentity(fields map[string]json.RawMessage) error {
	meta := map[string]json.RawMessage{}
	if raw, ok := fields["_meta"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &meta); err != nil || meta == nil {
			return fmt.Errorf("modern result _meta must be a JSON object")
		}
	}
	identity, err := json.Marshal(transport.ServerInfo{Name: "mini", Version: version.Version})
	if err != nil {
		return fmt.Errorf("encode mini server identity: %w", err)
	}
	addMissingField(meta, "io.modelcontextprotocol/serverInfo", identity)
	encoded, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode modern result metadata: %w", err)
	}
	fields["_meta"] = encoded
	return nil
}

func addMissingField(fields map[string]json.RawMessage, key string, value json.RawMessage) {
	if _, ok := fields[key]; !ok {
		fields[key] = value
	}
}

func (s *Server) handleDiscover() transport.DiscoverResult {
	return transport.DiscoverResult{
		ResultType:        string(transport.ResultComplete),
		SupportedVersions: transport.SupportedVersions(),
		Capabilities:      transport.ServerCapabilities{Tools: &transport.ToolsCapability{ListChanged: true}},
		Instructions:      proxyInitInstructions,
		Meta: &transport.DiscoverMeta{
			ServerInfo: transport.ServerInfo{Name: "mini", Version: version.Version},
		},
		CacheScope: "private",
	}
}

func requestEra(modern bool) transport.Era {
	if modern {
		return transport.EraModern
	}
	return transport.EraLegacy
}

func shapeResultForEra(method string, result any, era transport.Era) (any, error) {
	switch {
	case era == transport.EraModern:
		return addModernResultFields(method, result)
	case method == "tools/call":
		return legacyToolResult(result)
	}
	return result, nil
}

// Before 2026-07-28, structuredContent must be an object, and clients that validate results reject
// the whole call when an upstream on 2026-07-28 returns a list there:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/c4c367f9f58296a7053f5c78a52fd02bfbb56a49/schema/2025-11-25/schema.ts#L1110-L1113
func legacyToolResult(result any) (any, error) {
	raw, ok := result.(upstreamResult)
	if !ok {
		return result, nil
	}
	var doc toolResult
	if err := json.Unmarshal(raw, &doc); err != nil {
		return result, nil //nolint:nilerr // A result mini can't parse goes to the agent as the upstream sent it.
	}
	if isObjectJSON(doc.StructuredContent) {
		return result, nil
	}
	if !isJSONNull(doc.StructuredContent) && !doc.textMirrorsStructured() {
		doc.Content = append(doc.Content, newTextBlock(string(doc.StructuredContent)))
	}
	doc.StructuredContent = nil
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode legacy tool result: %w", err)
	}
	return upstreamResult(encoded), nil
}

func isObjectJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || trimmed[0] == '{'
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func (r toolResult) textMirrorsStructured() bool {
	structured, ok := decodeJSONValue(r.StructuredContent)
	if !ok {
		return false
	}
	for _, block := range r.Content {
		if block.Type != "text" || block.Text == nil {
			continue
		}
		if text, isJSON := trimmedJSONValue(*block.Text); isJSON && reflect.DeepEqual(text, structured) {
			return true
		}
	}
	return false
}

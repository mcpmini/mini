package transport

import "encoding/json"

type RequestMeta struct {
	ProtocolVersion    string              `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         *ClientInfo         `json:"io.modelcontextprotocol/clientInfo,omitempty"`
	ClientCapabilities *ClientCapabilities `json:"io.modelcontextprotocol/clientCapabilities,omitempty"`
}

type ClientCapabilities struct{}

type requestParamsMeta struct {
	Meta RequestMeta `json:"_meta"`
}

func ProtocolVersionFromParams(params json.RawMessage) (string, bool) {
	var request requestParamsMeta
	if json.Unmarshal(params, &request) != nil || request.Meta.ProtocolVersion == "" {
		return "", false
	}
	return request.Meta.ProtocolVersion, true
}

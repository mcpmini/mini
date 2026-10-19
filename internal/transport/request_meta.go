package transport

import "encoding/json"

type RequestMeta struct {
	ProtocolVersion    string              `json:"io.modelcontextprotocol/protocolVersion,omitempty"`
	ClientInfo         *ClientInfo         `json:"io.modelcontextprotocol/clientInfo,omitempty"`
	ClientCapabilities *ClientCapabilities `json:"io.modelcontextprotocol/clientCapabilities,omitempty"`
}

const clientCapabilitiesMeta = "io.modelcontextprotocol/clientCapabilities"

// ClientCapabilities decodes only the capability mini passes on, so other capabilities an agent
// declares never reach upstreams.
type ClientCapabilities struct {
	Elicitation json.RawMessage `json:"elicitation,omitempty"`
}

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

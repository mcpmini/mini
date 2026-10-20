package transport

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/version"
)

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

func prepareModernParams(params json.RawMessage, modernVersion string) (json.RawMessage, error) {
	fields, err := decodeObjectParams(params)
	if err != nil {
		return nil, err
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := fields["_meta"]; ok {
		if err = json.Unmarshal(raw, &meta); err != nil || meta == nil {
			return nil, fmt.Errorf("modern request _meta must be a JSON object")
		}
	}
	if err = addDefaultMeta(meta, modernVersion); err != nil {
		return nil, err
	}
	if fields["_meta"], err = json.Marshal(meta); err != nil {
		return nil, fmt.Errorf("encode modern request metadata: %w", err)
	}
	return json.Marshal(fields)
}

func addDefaultMeta(meta map[string]json.RawMessage, modernVersion string) error {
	encoded, err := json.Marshal(RequestMeta{
		ProtocolVersion:    modernVersion,
		ClientInfo:         &ClientInfo{Name: "mini", Version: version.Version},
		ClientCapabilities: &ClientCapabilities{},
	})
	if err != nil {
		return fmt.Errorf("encode modern request metadata: %w", err)
	}
	var defaults map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &defaults); err != nil {
		return fmt.Errorf("decode modern request metadata: %w", err)
	}
	for key, value := range defaults {
		if _, ok := meta[key]; !ok {
			meta[key] = value
		}
	}
	return nil
}

func decodeObjectParams(params json.RawMessage) (map[string]json.RawMessage, error) {
	if len(params) == 0 || string(bytes.TrimSpace(params)) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("modern request params must be a JSON object")
	}
	return fields, nil
}

func ProtocolVersionFromParams(params json.RawMessage) string {
	var request requestParamsMeta
	if json.Unmarshal(params, &request) != nil {
		return ""
	}
	return request.Meta.ProtocolVersion
}

package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const (
	protocolVersionMeta = "io.modelcontextprotocol/protocolVersion"
	clientInfoMeta      = "io.modelcontextprotocol/clientInfo"
)

// ValidateModernRequestMeta enforces required per-request metadata when modern intent is present.
func ValidateModernRequestMeta(params json.RawMessage) error {
	fields, err := decodeRequestParams(params)
	if err != nil {
		return err
	}
	metaRaw, present := fields["_meta"]
	if !present {
		return nil
	}
	meta, err := decodeRequestMeta(metaRaw)
	if err != nil {
		return err
	}
	if !hasModernRequestIntent(meta) {
		return nil
	}
	if err := validateProtocolVersion(meta[protocolVersionMeta]); err != nil {
		return err
	}
	if err := validateClientCapabilities(meta[clientCapabilitiesMeta]); err != nil {
		return err
	}
	return nil
}

func HasModernRequestIntent(params json.RawMessage) bool {
	fields, err := decodeRequestParams(params)
	if err != nil {
		return false
	}
	metaRaw, present := fields["_meta"]
	if !present {
		return false
	}
	meta, err := decodeRequestMeta(metaRaw)
	return err != nil || hasModernRequestIntent(meta)
}

func decodeRequestParams(params json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(params)) == 0 || bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		return nil, nil
	}
	if bytes.TrimSpace(params)[0] != '{' {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return nil, fmt.Errorf("request params must be valid JSON: %w", err)
	}
	if fields == nil {
		return map[string]json.RawMessage{}, nil
	}
	return fields, nil
}

func decodeRequestMeta(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &meta); err != nil || meta == nil {
		return nil, fmt.Errorf("_meta must be a JSON object")
	}
	return meta, nil
}

func hasModernRequestIntent(meta map[string]json.RawMessage) bool {
	_, hasVersion := meta[protocolVersionMeta]
	_, hasClientInfo := meta[clientInfoMeta]
	_, hasCapabilities := meta[clientCapabilitiesMeta]
	return hasVersion || hasClientInfo || hasCapabilities
}

func validateProtocolVersion(raw json.RawMessage) error {
	var version string
	if len(raw) == 0 || json.Unmarshal(raw, &version) != nil || version == "" {
		return fmt.Errorf("_meta.%s must be a nonempty string", protocolVersionMeta)
	}
	return nil
}

func validateClientCapabilities(raw json.RawMessage) error {
	var capabilities map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &capabilities) != nil || capabilities == nil {
		return fmt.Errorf("_meta.%s must be a JSON object", clientCapabilitiesMeta)
	}
	return nil
}

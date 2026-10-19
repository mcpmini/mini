package transport

import "encoding/json"

// ToolCallMRTR carries elicitation capabilities and opaque retry fields for modern tool calls.
type ToolCallMRTR struct {
	Elicitation    json.RawMessage
	InputResponses json.RawMessage
	RequestState   json.RawMessage
}

// ToolCallMeta decodes only the client capability mini passes on, so other capabilities an agent
// declares never reach upstreams.
type ToolCallMeta struct {
	ClientCapabilities *ToolCallCapabilities `json:"io.modelcontextprotocol/clientCapabilities,omitempty"`
}

type ToolCallCapabilities struct {
	Elicitation json.RawMessage `json:"elicitation,omitempty"`
}

func NewToolCallParams(name string, args map[string]any, mrtr *ToolCallMRTR) ToolCallParams {
	params := ToolCallParams{Name: name, Arguments: args}
	if mrtr == nil {
		return params
	}
	params.InputResponses = mrtr.InputResponses
	params.RequestState = mrtr.RequestState
	if len(mrtr.Elicitation) > 0 {
		params.Meta = &ToolCallMeta{ClientCapabilities: &ToolCallCapabilities{Elicitation: mrtr.Elicitation}}
	}
	return params
}

// stripModernToolCallFields keeps a modern agent's MRTR fields away from legacy upstreams, which
// would otherwise see request fields their protocol version doesn't define.
func stripModernToolCallFields(params json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return params
	}
	removedMeta := deleteMetaKey(fields, clientCapsMeta)
	removedFields := deleteKeys(fields, "inputResponses", "requestState")
	if !removedMeta && !removedFields {
		return params
	}
	stripped, err := json.Marshal(fields)
	if err != nil {
		return params
	}
	return stripped
}

func deleteKeys(fields map[string]json.RawMessage, keys ...string) bool {
	removed := false
	for _, key := range keys {
		if _, ok := fields[key]; ok {
			delete(fields, key)
			removed = true
		}
	}
	return removed
}

func deleteMetaKey(fields map[string]json.RawMessage, key string) bool {
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(fields["_meta"], &meta); err != nil || !deleteKeys(meta, key) {
		return false
	}
	if len(meta) == 0 {
		delete(fields, "_meta")
		return true
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return false
	}
	fields["_meta"] = encoded
	return true
}

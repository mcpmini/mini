package response

import "github.com/mcpmini/mini/internal/projection"

// ProxyMsg is shown in ProjectionMeta whenever projection excluded or
// truncated fields, pointing the agent at read() for full recovery.
const ProxyMsg = "Response filtered, some fields were excluded or truncated, use read(<file>, <jq filter>) to fetch full values."

// ProxyResult is the stable envelope every successful flat-proxy tool call
// returns. Data is never omitted, even when nil, so callers can always find
// the result at the same JSON path.
type ProxyResult struct {
	Data any             `json:"data"`
	Mini *ProjectionMeta `json:"__mini,omitempty"`
}

type ProjectionMeta struct {
	Msg         string                  `json:"msg,omitempty"`
	File        string                  `json:"file,omitempty"`
	Excluded    []string                `json:"excluded,omitempty"`
	Truncated   []projection.Truncation `json:"truncated,omitempty"`
	Passthrough map[string]any          `json:"passthrough,omitempty"`
}

// NewProxyResult builds the stable proxy envelope from an internal Envelope.
// __mini is omitted entirely when projection left the response unaltered.
func NewProxyResult(env *Envelope) ProxyResult {
	return ProxyResult{Data: env.Data, Mini: buildProjectionMeta(env)}
}

func buildProjectionMeta(env *Envelope) *ProjectionMeta {
	if !envelopeWasAltered(env) {
		return nil
	}
	meta := &ProjectionMeta{
		Excluded:    env.Excluded,
		Truncated:   env.Truncated,
		Passthrough: env.Passthrough,
	}
	if env.File != nil {
		meta.File = *env.File
	}
	if len(env.Excluded) > 0 || len(env.Truncated) > 0 {
		meta.Msg = ProxyMsg
	}
	return meta
}

func envelopeWasAltered(env *Envelope) bool {
	return len(env.Excluded) > 0 || len(env.Truncated) > 0 || len(env.Passthrough) > 0 || env.File != nil
}

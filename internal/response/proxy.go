package response

import "github.com/mcpmini/mini/internal/projection"

const ProxyMsg = "Response filtered, some fields were excluded or truncated, use read(<file>, <jq filter>) to fetch full values."

// Data uses json:"data" without omitempty so null serializes as {"data":null}.
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
	return len(env.Excluded) > 0 || len(env.Truncated) > 0
}

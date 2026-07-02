package response

import "github.com/mcpmini/mini/internal/projection"

// Envelope is what agents receive for every execute call.
// On success: Data is set. On error: Error and Message are set.
type Envelope struct {
	Data any `json:"data,omitempty"`

	Excluded  []string                `json:"excluded,omitempty"`
	Truncated []projection.Truncation `json:"truncated,omitempty"`

	// File is the path to the full raw upstream response, set when any
	// projection was applied.
	File *string `json:"file,omitempty"`

	// Passthrough contains fields explicitly preserved from projection rules.
	Passthrough map[string]any `json:"passthrough,omitempty"`

	// Error fields (set on failure only).
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Action    string `json:"action,omitempty"`
}

// CallStats tracks per-call size info internally — never sent to agents.
type CallStats struct {
	RawTokens     int
	SummaryTokens int
}

func (s CallStats) ReductionPct() float64 {
	if s.RawTokens == 0 {
		return 0
	}
	return float64(s.RawTokens-s.SummaryTokens) / float64(s.RawTokens) * 100
}

const ProxyMsg = "Response filtered, some fields were excluded or truncated, use read(<file>, <jq filter>) to fetch full values."

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

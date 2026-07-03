package response

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/projection"
)

// Envelope is what agents receive for every execute call.
// On success: Data is set. On error: Error and Message are set.
type Envelope struct {
	Data any

	Excluded  []string
	Truncated []projection.Truncation

	// File is the path to the full raw upstream response, set when any
	// projection was applied.
	File *string

	// Passthrough contains fields explicitly preserved from projection rules.
	Passthrough map[string]any

	// Error fields (set on failure only).
	Error     string
	Message   string
	Retryable bool
	Action    string
}

// MarshalJSON keeps "data" out of the wire shape entirely on error — even
// "data":null would blur the isError signal agents rely on to skip past a
// failed call without inspecting its payload.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if e.Error != "" {
		return json.Marshal(envelopeErrorJSON{
			Error: e.Error, Message: e.Message, Retryable: e.Retryable, Action: e.Action,
		})
	}
	return json.Marshal(envelopeSuccessJSON{
		Data: e.Data, Excluded: e.Excluded, Truncated: e.Truncated,
		File: e.File, Passthrough: e.Passthrough,
	})
}

type envelopeSuccessJSON struct {
	Data        any                     `json:"data"`
	Excluded    []string                `json:"excluded,omitempty"`
	Truncated   []projection.Truncation `json:"truncated,omitempty"`
	File        *string                 `json:"file,omitempty"`
	Passthrough map[string]any          `json:"passthrough,omitempty"`
}

type envelopeErrorJSON struct {
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

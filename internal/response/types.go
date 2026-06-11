package response

import "github.com/mcpmini/mini/internal/projection"

// Omission records a field that was truncated or replaced with a
// placeholder during projection, with bytes removed and a path pointer
// into Data for locating the full value in the raw file.
type Omission = projection.Omission

// Envelope is what agents receive for every execute call.
// On success: Data is set. On error: Error and Message are set.
type Envelope struct {
	// Data is the projected response (success only). Always set, even when
	// a raw file is also written.
	Data any `json:"data,omitempty"`

	// Elided lists field names stripped by the projection engine.
	Elided []string `json:"elided,omitempty"`

	// Omitted lists fields that were truncated or replaced with a
	// placeholder, with the bytes removed and a path into Data.
	Omitted []Omission `json:"omitted,omitempty"`

	// Hint is a steering note from the projection config (e.g. pointing
	// agents at a related tool for full data).
	Hint string `json:"hint,omitempty"`

	// File is the path to the full raw upstream response, set when Omitted
	// or Elided is non-empty.
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

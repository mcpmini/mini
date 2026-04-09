package response

// Envelope is what agents receive for every execute call.
// On success: Data is set. On error: Error and Message are set.
type Envelope struct {
	// Data is the projected response (success only).
	Data any `json:"data,omitempty"`

	// Elided lists field names stripped by the projection engine.
	Elided []string `json:"elided,omitempty"`

	// Truncated maps field names to bytes removed by string limits.
	Truncated map[string]int `json:"truncated,omitempty"`

	// File is the path to the full raw upstream response, set when any
	// projection (elision or truncation) was applied.
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

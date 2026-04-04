package response

// Envelope is what agents receive for every execute call.
type Envelope struct {
	OK bool `json:"ok"`

	// Data is the projected response (ok=true only).
	Data any `json:"data,omitempty"`

	// Elided lists field names stripped by the projection engine.
	Elided []string `json:"elided,omitempty"`

	// File is set when the full response was written to disk (too large to inline).
	File *string `json:"file,omitempty"`

	// Passthrough contains fields explicitly preserved from projection rules.
	Passthrough map[string]any `json:"passthrough,omitempty"`

	// Token estimates — approximations only, not Claude API counts.
	EstimatedRawTokens  int `json:"estimated_raw_tokens,omitempty"`
	EstimatedTokensSaved int `json:"estimated_tokens_saved,omitempty"`

	// LatencyMs is the upstream tool call duration in milliseconds.
	LatencyMs int64 `json:"latency_ms,omitempty"`

	// Error fields (ok=false only).
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

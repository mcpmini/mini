package response

import (
	"encoding/json"
	"fmt"
)

type Builder struct {
	store     *Store
	threshold int
}

// NewBuilder creates a Builder that writes a raw response file when the
// projected summary exceeds threshold tokens, or when fields were elided
// or omitted (regardless of size).
func NewBuilder(store *Store, threshold int) *Builder {
	return &Builder{store: store, threshold: threshold}
}

// BuildParams holds inputs for Builder.Build.
type BuildParams struct {
	Server      string
	Tool        string
	Raw         json.RawMessage
	Summary     any
	Elided      []string
	Omitted     []Omission
	Hint        string
	Passthrough map[string]any
}

func (b *Builder) Build(p BuildParams) (*Envelope, CallStats, error) {
	rawTokens := EstimateTokensRaw(p.Raw)
	summaryTokens := EstimateTokens(p.Summary)
	stats := CallStats{RawTokens: rawTokens, SummaryTokens: summaryTokens}
	e := newEnvelope(p)
	if summaryTokens > b.threshold || len(p.Elided) > 0 || len(p.Omitted) > 0 {
		if err := b.writeRawFile(e, p); err != nil {
			return nil, stats, err
		}
	}
	return e, stats, nil
}

func newEnvelope(p BuildParams) *Envelope {
	return &Envelope{
		Data:        p.Summary,
		Elided:      nilIfEmpty(p.Elided),
		Omitted:     p.Omitted,
		Hint:        p.Hint,
		Passthrough: nilIfEmptyMap(p.Passthrough),
	}
}

func (b *Builder) writeRawFile(e *Envelope, p BuildParams) error {
	path, err := b.store.WriteRaw(p.Raw)
	if err != nil {
		return fmt.Errorf("store response: %w", err)
	}
	e.File = &path
	return nil
}

func BuildError(errCode, message string, retryable bool, action string) *Envelope {
	return &Envelope{
		Error:     errCode,
		Message:   message,
		Retryable: retryable,
		Action:    action,
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	return m
}

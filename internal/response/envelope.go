package response

import (
	"encoding/json"
	"fmt"
)

type Builder struct {
	store           *Store
	inlineThreshold int
}

func NewBuilder(store *Store, inlineThreshold int) *Builder {
	return &Builder{store: store, inlineThreshold: inlineThreshold}
}

// BuildParams holds inputs for Builder.Build.
type BuildParams struct {
	Server      string
	Tool        string
	Raw         json.RawMessage
	Summary     any
	Elided      []string
	Passthrough map[string]any
}

func (b *Builder) Build(p BuildParams) (*Envelope, CallStats, error) {
	rawTokens := EstimateTokensRaw(p.Raw)
	summaryTokens := EstimateTokens(p.Summary)
	stats := CallStats{RawTokens: rawTokens, SummaryTokens: summaryTokens}
	e := newEnvelope(p, rawTokens, summaryTokens)
	if summaryTokens > b.inlineThreshold {
		if err := b.writeToFile(e, p); err != nil {
			return nil, stats, err
		}
	} else {
		e.Data = p.Summary
	}
	return e, stats, nil
}

func newEnvelope(p BuildParams, rawTokens, summaryTokens int) *Envelope {
	saved := rawTokens - summaryTokens
	if saved < 0 {
		saved = 0
	}
	return &Envelope{
		OK:                   true,
		Elided:               nilIfEmpty(p.Elided),
		Passthrough:          nilIfEmptyMap(p.Passthrough),
		EstimatedRawTokens:   rawTokens,
		EstimatedTokensSaved: saved,
	}
}

func (b *Builder) writeToFile(e *Envelope, p BuildParams) error {
	path, err := b.store.WritePair(Slimify(p.Summary), p.Raw)
	if err != nil {
		return fmt.Errorf("store response: %w", err)
	}
	e.File = &path
	return nil
}

func BuildError(errCode, message string, retryable bool, action string) *Envelope {
	return &Envelope{
		OK:        false,
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

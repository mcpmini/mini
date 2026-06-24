package response

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/projection"
)

type Builder struct {
	store *Store
}

func NewBuilder(store *Store) *Builder {
	return &Builder{store: store}
}

// BuildParams holds inputs for Builder.Build.
type BuildParams struct {
	Server      string
	Tool        string
	Raw         json.RawMessage
	Summary     any
	Excluded    []string
	Truncated   []projection.Truncation
	Passthrough map[string]any
}

func (b *Builder) Build(p BuildParams) (*Envelope, CallStats, error) {
	rawTokens := EstimateTokensRaw(p.Raw)
	summaryTokens := EstimateTokens(p.Summary)
	stats := CallStats{RawTokens: rawTokens, SummaryTokens: summaryTokens}
	e := newEnvelope(p)
	if len(p.Excluded) > 0 || len(p.Truncated) > 0 {
		if err := b.writeRawFile(e, p); err != nil {
			return nil, stats, err
		}
	}
	return e, stats, nil
}

func newEnvelope(p BuildParams) *Envelope {
	return &Envelope{
		Data:        p.Summary,
		Excluded:    nilIfEmpty(p.Excluded),
		Truncated:   nilIfEmptyTruncated(p.Truncated),
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

func nilIfEmptyTruncated(t []projection.Truncation) []projection.Truncation {
	if len(t) == 0 {
		return nil
	}
	return t
}

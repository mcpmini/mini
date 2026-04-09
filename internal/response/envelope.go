package response

import (
	"encoding/json"
	"fmt"
)

type Builder struct {
	store     *Store
	threshold int
}

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
	Truncated   map[string]int
	Passthrough map[string]any
}

func (b *Builder) Build(p BuildParams) (*Envelope, CallStats, error) {
	rawTokens := EstimateTokensRaw(p.Raw)
	summaryTokens := EstimateTokens(p.Summary)
	stats := CallStats{RawTokens: rawTokens, SummaryTokens: summaryTokens}
	e := &Envelope{
		Data:        p.Summary,
		Elided:      nilIfEmpty(p.Elided),
		Truncated:   nilIfEmptyIntMap(p.Truncated),
		Passthrough: nilIfEmptyMap(p.Passthrough),
	}
	if rawTokens > b.threshold || anyProjectionApplied(p) {
		if err := b.writeFiles(e, p); err != nil {
			return nil, stats, err
		}
	}
	return e, stats, nil
}

// anyProjectionApplied reports whether the response was filtered in any way.
func anyProjectionApplied(p BuildParams) bool {
	return len(p.Elided) > 0 || len(p.Truncated) > 0
}

func (b *Builder) writeFiles(e *Envelope, p BuildParams) error {
	path, err := b.store.WritePair(Slimify(p.Summary), p.Raw)
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

func nilIfEmptyIntMap(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	return m
}

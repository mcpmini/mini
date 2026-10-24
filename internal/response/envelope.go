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
	Server    string
	Tool      string
	Raw       json.RawMessage
	Summary   any
	Excluded  []string
	Truncated []projection.Truncation
}

func (b *Builder) Build(p BuildParams) (*Envelope, error) {
	e := newEnvelope(p)
	if len(p.Excluded) > 0 || len(p.Truncated) > 0 {
		if err := b.writeRawFile(e, p); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func newEnvelope(p BuildParams) *Envelope {
	return &Envelope{
		Data:      p.Summary,
		Excluded:  nilIfEmpty(p.Excluded),
		Truncated: nilIfEmptyTruncated(p.Truncated),
	}
}

func (b *Builder) writeRawFile(e *Envelope, p BuildParams) error {
	key, err := b.store.WriteRaw(p.Raw)
	if err != nil {
		return fmt.Errorf("store response: %w", err)
	}
	e.File = &key
	return nil
}

type BuildErrorParams struct {
	Code      string
	Message   string
	Retryable bool
	Action    string
}

func BuildError(p BuildErrorParams) *Envelope {
	return &Envelope{
		Error:     p.Code,
		Message:   p.Message,
		Retryable: p.Retryable,
		Action:    p.Action,
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyTruncated(t []projection.Truncation) []projection.Truncation {
	if len(t) == 0 {
		return nil
	}
	return t
}

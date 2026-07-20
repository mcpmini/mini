// Package toon encodes values into TOON (Token-Oriented Object Notation),
// spec v3.3, pinned to commit f55b93ac489f297ff597d95e4c19ae84675eaeb7 of
// https://github.com/toon-format/spec.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md
package toon

// Kind is the discriminant for Value.Kind. The zero value is intentionally
// invalid; Encode rejects it as unknown.
type Kind int

const (
	KindNull Kind = iota + 1
	KindBool
	KindNumber
	KindString
	KindObject
	KindArray
)

// Value is a closed value model mirroring the JSON data model.
type Value struct {
	Kind Kind

	Bool bool
	// Num holds a canonicalized number lexeme (text, not a parsed value).
	Num string
	Str string

	Fields []Field // preserves document order
	Items  []Value
}

// Field is a single key/value pair of a KindObject Value, in document order.
type Field struct {
	Key string
	Val Value
}

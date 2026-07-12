// Package toon encodes values into TOON (Token-Oriented Object Notation),
// spec v3.3, pinned to commit f55b93ac489f297ff597d95e4c19ae84675eaeb7 of
// https://github.com/toon-format/spec.
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

// Value is a closed value model mirroring the JSON data model. Encoding is a
// switch on Kind rather than an interface dispatch.
type Value struct {
	Kind Kind

	Bool bool
	// Num holds a canonicalized number lexeme, set by FromJSON/FromAny.
	Num string
	Str string

	Fields []Field // KindObject; preserves document order
	Items  []Value // KindArray
}

// Field is a single key/value pair of a KindObject Value, in document order.
type Field struct {
	Key string
	Val Value
}

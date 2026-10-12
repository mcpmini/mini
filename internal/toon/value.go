// Package toon encodes values into TOON (Token-Oriented Object Notation),
// spec v4.1, pinned to release v4.1.1 of https://github.com/toon-format/spec.
// https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L1
package toon

type valueKind int

const (
	KindNull valueKind = iota + 1
	KindBool
	KindNumber
	KindString
	KindObject
	KindArray
)

// Value has an invalid zero value; Encode rejects it.
type Value struct {
	payload valuePayload
}

//sumtype:decl
type valuePayload interface {
	valuePayload()
}

type (
	nullValue   struct{}
	boolValue   bool
	numberValue string
	stringValue string
	objectValue []Field
	arrayValue  []Value
)

func (nullValue) valuePayload()   {}
func (boolValue) valuePayload()   {}
func (numberValue) valuePayload() {}
func (stringValue) valuePayload() {}
func (objectValue) valuePayload() {}
func (arrayValue) valuePayload()  {}

func (v Value) kind() valueKind {
	switch v.payload.(type) {
	case nullValue:
		return KindNull
	case boolValue:
		return KindBool
	case numberValue:
		return KindNumber
	case stringValue:
		return KindString
	case objectValue:
		return KindObject
	case arrayValue:
		return KindArray
	default:
		return 0
	}
}

func (v Value) fields() []Field {
	fields, _ := v.payload.(objectValue)
	return fields
}

func (v Value) items() []Value {
	items, _ := v.payload.(arrayValue)
	return items
}

type Field struct {
	Key string
	Val Value
}

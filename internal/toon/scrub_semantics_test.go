package toon

import (
	"encoding"
	"math"
	"testing"
)

type scrubTextValue struct {
	Raw string
}

func (v scrubTextValue) MarshalText() ([]byte, error) {
	return []byte("encoded:" + v.Raw), nil
}

type scrubPointerValue struct {
	Raw string
}

func (*scrubPointerValue) MarshalJSON() ([]byte, error) {
	return []byte(`"pointer"`), nil
}

type scrubZeroValue struct {
	zero bool
}

func (v scrubZeroValue) IsZero() bool {
	return v.zero
}

var _ encoding.TextMarshaler = scrubTextValue{}

func TestFromAnyFallbackPreservesTextMarshaler(t *testing.T) {
	type record struct {
		Bad float64        `json:"bad"`
		Text scrubTextValue `json:"text"`
	}
	v, err := FromAny(record{Bad: math.NaN(), Text: scrubTextValue{Raw: "source"}})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)["text"]
	if got.Kind != KindString || got.Str != "encoded:source" {
		t.Fatalf("text = %+v, want encoded text", got)
	}
}

func TestFromAnyFallbackPreservesPointerMarshalerAddressability(t *testing.T) {
	type record struct {
		Bad float64           `json:"bad"`
		Value scrubPointerValue `json:"value"`
	}
	value, err := FromAny(record{Bad: math.NaN(), Value: scrubPointerValue{Raw: "source"}})
	if err != nil {
		t.Fatalf("value FromAny unexpected error: %v", err)
	}
	if got := fieldMap(value)["value"]; got.Kind != KindObject {
		t.Fatalf("value field = %+v, want object for unaddressable value", got)
	}

	pointer, err := FromAny(&record{Bad: math.NaN(), Value: scrubPointerValue{Raw: "source"}})
	if err != nil {
		t.Fatalf("pointer FromAny unexpected error: %v", err)
	}
	if got := fieldMap(pointer)["value"]; got.Kind != KindString || got.Str != "pointer" {
		t.Fatalf("pointer field = %+v, want pointer marshaler output", got)
	}
}

func TestFromAnyFallbackPreservesJSONStringAndOmitZero(t *testing.T) {
	type record struct {
		Bad         float64        `json:"bad"`
		BadString   float64        `json:"bad_string,string"`
		Count       int            `json:"count,string"`
		Zero        int            `json:"zero,omitzero"`
		Nonzero     int            `json:"nonzero,omitzero"`
		CustomZero scrubZeroValue `json:"custom,omitzero"`
	}
	v, err := FromAny(record{Bad: math.NaN(), BadString: math.NaN(), Count: 42, Nonzero: 3, CustomZero: scrubZeroValue{zero: true}})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	fields := fieldMap(v)
	if got := fields["count"]; got.Kind != KindString || got.Str != "42" {
		t.Fatalf("count = %+v, want quoted number", got)
	}
	if got := fields["bad_string"]; got.Kind != KindNull {
		t.Fatalf("bad_string = %+v, want null", got)
	}
	if _, ok := fields["zero"]; ok {
		t.Fatal("zero omitzero field was emitted")
	}
	if got := fields["nonzero"]; got.Kind != KindNumber || got.Num != "3" {
		t.Fatalf("nonzero = %+v, want number 3", got)
	}
	if _, ok := fields["custom"]; ok {
		t.Fatal("custom zero field was emitted")
	}
}

func TestFromAnyFallbackRejectsCycles(t *testing.T) {
	type node struct {
		Bad  float64 `json:"bad"`
		Next *node   `json:"next"`
	}
	root := &node{Bad: math.NaN()}
	root.Next = root
	if _, err := FromAny(root); err == nil {
		t.Fatal("FromAny expected a cycle error")
	}
}

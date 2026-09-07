package toon

import (
	"encoding"
	"encoding/json"
	"math"
	"strings"
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

type scrubPointerTextValue struct {
	Raw string
}

func (v *scrubPointerTextValue) MarshalText() ([]byte, error) {
	return []byte("encoded:" + v.Raw), nil
}

type scrubNestedValue struct {
	Value scrubPointerValue     `json:"value"`
	Text  scrubPointerTextValue `json:"text"`
	Items [1]scrubPointerValue  `json:"items"`
}

type scrubNestedRoot struct {
	Bad    float64          `json:"bad"`
	Nested scrubNestedValue `json:"nested"`
}

type scrubZeroValue struct {
	zero bool
}

func (v scrubZeroValue) IsZero() bool {
	return v.zero
}

type scrubNilZeroValue struct {
	zero bool
}

func (v *scrubNilZeroValue) IsZero() bool {
	return v.zero
}

type scrubZeroer interface {
	IsZero() bool
}

var _ encoding.TextMarshaler = scrubTextValue{}

func TestFromAnyFallbackPreservesTextMarshaler(t *testing.T) {
	type record struct {
		Bad  float64        `json:"bad"`
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
		Bad   float64           `json:"bad"`
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

func TestFromAnyFallbackPreservesNestedPointerMarshalers(t *testing.T) {
	newRoot := func(bad float64) scrubNestedRoot {
		return scrubNestedRoot{
			Bad: bad,
			Nested: scrubNestedValue{
				Value: scrubPointerValue{Raw: "source"},
				Text:  scrubPointerTextValue{Raw: "source"},
				Items: [1]scrubPointerValue{{Raw: "source"}},
			},
		}
	}
	cases := []struct {
		name      string
		input     func() any
		finite    func() any
		wantHooks bool
	}{
		{name: "pointer record", input: func() any { v := newRoot(math.NaN()); return &v }, finite: func() any { v := newRoot(1); return &v }, wantHooks: true},
		{name: "record value", input: func() any { return newRoot(math.NaN()) }, finite: func() any { return newRoot(1) }},
		{name: "map value", input: func() any { return map[string]scrubNestedRoot{"root": newRoot(math.NaN())} }, finite: func() any { return map[string]scrubNestedRoot{"root": newRoot(1)} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseline, err := json.Marshal(tc.finite())
			if err != nil {
				t.Fatalf("json.Marshal baseline: %v", err)
			}
			wantHooks := string(baseline)
			if tc.wantHooks != (len(wantHooks) > 0 && containsAll(wantHooks, `"pointer"`, `"encoded:source"`)) {
				t.Fatalf("baseline = %s, want hooks = %t", baseline, tc.wantHooks)
			}
			got, err := FromAny(tc.input())
			if err != nil {
				t.Fatalf("FromAny unexpected error: %v", err)
			}
			if tc.name == "map value" {
				got = fieldMap(got)["root"]
			}
			fields := fieldMap(got)
			if fields["bad"].Kind != KindNull {
				t.Fatalf("bad = %+v, want KindNull", fields["bad"])
			}
			nested := fields["nested"]
			nestedFields := fieldMap(nested)
			if tc.wantHooks {
				if nestedFields["value"].Kind != KindString || nestedFields["value"].Str != "pointer" {
					t.Fatalf("nested value = %+v, want pointer string", nestedFields["value"])
				}
				if nestedFields["text"].Kind != KindString || nestedFields["text"].Str != "encoded:source" {
					t.Fatalf("nested text = %+v, want encoded text", nestedFields["text"])
				}
				items := nestedFields["items"]
				if items.Items[0].Kind != KindString || items.Items[0].Str != "pointer" {
					t.Fatalf("nested item = %+v, want pointer string", items.Items[0])
				}
			} else if nestedFields["value"].Kind != KindObject || nestedFields["text"].Kind != KindObject || nestedFields["items"].Items[0].Kind != KindObject {
				t.Fatalf("unaddressable nested value retained pointer hooks: %+v", nested)
			}
		})
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func TestFromAnyFallbackPreservesJSONStringAndOmitZero(t *testing.T) {
	type record struct {
		Bad        float64        `json:"bad"`
		BadString  float64        `json:"bad_string,string"`
		Count      int            `json:"count,string"`
		Zero       int            `json:"zero,omitzero"`
		Nonzero    int            `json:"nonzero,omitzero"`
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

func TestFromAnyFallbackOmitZeroHandlesNilInterfaceValues(t *testing.T) {
	type record struct {
		Bad   float64     `json:"bad"`
		Value scrubZeroer `json:"value,omitzero"`
	}
	nilValue := (*scrubNilZeroValue)(nil)
	cases := []struct {
		name  string
		value scrubZeroer
		omit  bool
	}{
		{name: "nil interface", omit: true},
		{name: "typed nil pointer", value: nilValue, omit: true},
		{name: "non-nil zero", value: &scrubNilZeroValue{zero: true}, omit: true},
		{name: "non-nil nonzero", value: &scrubNilZeroValue{zero: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, input := range []struct {
				name string
				v    any
			}{
				{name: "record", v: record{Bad: math.NaN(), Value: tc.value}},
				{name: "pointer", v: &record{Bad: math.NaN(), Value: tc.value}},
			} {
				t.Run(input.name, func(t *testing.T) {
					finite := record{Bad: 1, Value: tc.value}
					baseline, err := json.Marshal(finite)
					if err != nil {
						t.Fatalf("json.Marshal baseline: %v", err)
					}
					var want map[string]json.RawMessage
					if err := json.Unmarshal(baseline, &want); err != nil {
						t.Fatalf("json.Unmarshal baseline: %v", err)
					}
					_, wantValue := want["value"]
					if wantValue == tc.omit {
						t.Fatalf("baseline value presence = %t, want omit %t", wantValue, tc.omit)
					}

					got, err := FromAny(input.v)
					if err != nil {
						t.Fatalf("FromAny unexpected error: %v", err)
					}
					fields := fieldMap(got)
					if fields["bad"].Kind != KindNull {
						t.Fatalf("bad = %+v, want KindNull", fields["bad"])
					}
					_, gotValue := fields["value"]
					if gotValue != wantValue {
						t.Fatalf("value presence = %t, want baseline %t", gotValue, wantValue)
					}
				})
			}
		})
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

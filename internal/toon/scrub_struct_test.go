package toon

import (
	"math"
	"testing"
)

func TestFromAnyTier3OmitemptyPreserved(t *testing.T) {
	type record struct {
		F   float64 `json:"f"`
		Tag string  `json:"tag,omitempty"`
	}
	v, err := FromAny(record{F: math.NaN(), Tag: ""})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["f"].Kind != KindNull {
		t.Errorf("f = %+v, want KindNull", got["f"])
	}
	if _, ok := got["tag"]; ok {
		t.Error("tag present; tier 3 must honor omitempty for zero-value fields")
	}
}

func TestFromAnyTier3OmitemptyMatchesEncodingJSONEmptyKinds(t *testing.T) {
	type record struct {
		F       float64        `json:"f"`
		Slice   []int          `json:"slice,omitempty"`
		Map     map[string]int `json:"map,omitempty"`
		String  string         `json:"string,omitempty"`
		Array   [0]int         `json:"array,omitempty"`
		Dynamic any            `json:"dynamic,omitempty"`
	}
	v, err := FromAny(record{F: math.NaN(), Slice: []int{}, Map: map[string]int{}, Dynamic: []int{}})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["f"].Kind != KindNull {
		t.Errorf("f = %+v, want KindNull", got["f"])
	}
	for _, key := range []string{"slice", "map", "string", "array"} {
		if _, ok := got[key]; ok {
			t.Errorf("omitempty field %q present", key)
		}
	}
	if got["dynamic"].Kind != KindArray || len(got["dynamic"].Items) != 0 {
		t.Errorf("dynamic = %+v, want present empty array; non-nil interface is not empty", got["dynamic"])
	}
}

func TestFromAnyTier3KeepsNonOmitJSONOption(t *testing.T) {
	type record struct {
		F     float64 `json:"f"`
		Value string  `json:"value,string"`
	}
	v, err := FromAny(record{F: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["value"].Kind != KindString || got["value"].Str != "" {
		t.Errorf("value = %+v, want empty string field retained", got["value"])
	}
}

func TestHasJSONOptionUsesExactTokens(t *testing.T) {
	if hasJSONOption("notomitempty", "omitempty") {
		t.Error("similar option name must not match omitempty")
	}
	if !hasJSONOption("string,omitempty", "omitempty") {
		t.Error("comma-delimited omitempty option was not recognized")
	}
}

func TestFromAnyTier3PromotesEmbeddedFields(t *testing.T) {
	type Embedded struct {
		Promoted string `json:"promoted"`
	}
	type record struct {
		Embedded
		F float64 `json:"f"`
	}
	v, err := FromAny(record{Embedded: Embedded{Promoted: "ok"}, F: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["promoted"].Kind != KindString || got["promoted"].Str != "ok" {
		t.Errorf("promoted = %+v, want string ok", got["promoted"])
	}
	if _, ok := got["embedded"]; ok {
		t.Error("embedded field was not promoted")
	}
}

func TestFromAnyTier3KeepsTaggedAnonymousFieldNested(t *testing.T) {
	type Embedded struct {
		Promoted string `json:"promoted"`
	}
	type record struct {
		Embedded `json:"embedded"`
		F        float64 `json:"f"`
	}
	v, err := FromAny(record{Embedded: Embedded{Promoted: "ok"}, F: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	inner := got["embedded"]
	if inner.Kind != KindObject || fieldMap(inner)["promoted"].Str != "ok" {
		t.Errorf("embedded = %+v, want nested object with promoted field", inner)
	}
}

func TestFromAnyTier3SkipsPromotedFieldsThroughNilAnonymousPointer(t *testing.T) {
	type Embedded struct {
		Promoted string `json:"promoted"`
	}
	type record struct {
		*Embedded
		F float64 `json:"f"`
	}
	v, err := FromAny(record{F: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if _, ok := got["promoted"]; ok {
		t.Error("promoted field from nil anonymous pointer should be omitted")
	}
}

func TestFromAnyTier3UsesEncodingJSONConflictPrecedence(t *testing.T) {
	type Left struct {
		Value string
	}
	type Right struct {
		Value string
	}
	type record struct {
		Left
		Right
		Value string  `json:"value"`
		F     float64 `json:"f"`
	}
	v, err := FromAny(record{
		Left: Left{Value: "left"}, Right: Right{Value: "right"},
		Value: "outer", F: math.NaN(),
	})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["value"].Kind != KindString || got["value"].Str != "outer" {
		t.Errorf("value = %+v, want outer field to dominate embedded conflicts", got["value"])
	}
}

func TestFromAnyTier3TaggedFieldDominatesUntaggedSameDepth(t *testing.T) {
	type Tagged struct {
		Value string `json:"value"`
	}
	type Untagged struct {
		Value string
	}
	type record struct {
		Tagged
		Untagged
		F float64 `json:"f"`
	}
	v, err := FromAny(record{
		Tagged: Tagged{Value: "tagged"}, Untagged: Untagged{Value: "untagged"}, F: math.NaN(),
	})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["value"].Kind != KindString || got["value"].Str != "tagged" {
		t.Errorf("value = %+v, want tagged field to dominate", got["value"])
	}
}

func TestFromAnyTier3OmitsAmbiguousEmbeddedConflict(t *testing.T) {
	type Left struct {
		Value string
	}
	type Right struct {
		Value string
	}
	type record struct {
		Left
		Right
		F float64 `json:"f"`
	}
	v, err := FromAny(record{
		Left: Left{Value: "left"}, Right: Right{Value: "right"}, F: math.NaN(),
	})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	if _, ok := fieldMap(v)["value"]; ok {
		t.Error("ambiguous same-depth fields should both be omitted")
	}
}

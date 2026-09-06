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
	if got["value"].Kind != KindString || got["value"].Str != `""` {
		t.Errorf("value = %+v, want JSON string literal retained", got["value"])
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

func TestFromAnyTier3FieldOrder(t *testing.T) {
	t.Run("top-level struct preserves declaration order", func(t *testing.T) {
		type record struct {
			Z   int     `json:"z"`
			Bad float64 `json:"bad"`
			A   int     `json:"a"`
		}
		v, err := FromAny(record{Z: 1, Bad: math.NaN(), A: 2})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		wantOrder := []string{"z", "bad", "a"}
		if len(v.Fields) != len(wantOrder) {
			t.Fatalf("got %d fields, want %d", len(v.Fields), len(wantOrder))
		}
		for i, want := range wantOrder {
			if v.Fields[i].Key != want {
				t.Errorf("field[%d] = %q, want %q (declaration order z,bad,a; broken code sorts a,bad,z)", i, v.Fields[i].Key, want)
			}
		}
	})

	t.Run("struct inside slice preserves declaration order", func(t *testing.T) {
		type item struct {
			Z   int     `json:"z"`
			Bad float64 `json:"bad"`
			A   int     `json:"a"`
		}
		v, err := FromAny(map[string]any{"items": []item{{Z: 1, Bad: math.NaN(), A: 2}}})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		elem := fieldMap(v)["items"].Items[0]
		wantOrder := []string{"z", "bad", "a"}
		for i, want := range wantOrder {
			if elem.Fields[i].Key != want {
				t.Errorf("slice elem field[%d] = %q, want %q", i, elem.Fields[i].Key, want)
			}
		}
	})

	t.Run("struct inside struct preserves declaration order", func(t *testing.T) {
		type inner struct {
			Z   int     `json:"z"`
			Bad float64 `json:"bad"`
			A   int     `json:"a"`
		}
		type outer struct {
			B     string `json:"b"`
			Inner inner  `json:"inner"`
		}
		v, err := FromAny(outer{B: "x", Inner: inner{Z: 1, Bad: math.NaN(), A: 2}})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		nested := fieldMap(v)["inner"]
		wantOrder := []string{"z", "bad", "a"}
		for i, want := range wantOrder {
			if nested.Fields[i].Key != want {
				t.Errorf("nested field[%d] = %q, want %q", i, nested.Fields[i].Key, want)
			}
		}
	})

	t.Run("struct inside map value preserves declaration order", func(t *testing.T) {
		type item struct {
			Z   int     `json:"z"`
			Bad float64 `json:"bad"`
			A   int     `json:"a"`
		}
		v, err := FromAny(map[string]any{"key": item{Z: 1, Bad: math.NaN(), A: 2}})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		nested := fieldMap(v)["key"]
		wantOrder := []string{"z", "bad", "a"}
		for i, want := range wantOrder {
			if nested.Fields[i].Key != want {
				t.Errorf("map value field[%d] = %q, want %q", i, nested.Fields[i].Key, want)
			}
		}
	})

	t.Run("deeply nested struct preserves declaration order", func(t *testing.T) {
		type leaf struct {
			Z   int     `json:"z"`
			Bad float64 `json:"bad"`
			A   int     `json:"a"`
		}
		type mid struct {
			Items []leaf `json:"items"`
		}
		type root struct {
			Mid mid `json:"mid"`
		}
		v, err := FromAny(root{Mid: mid{Items: []leaf{{Z: 1, Bad: math.NaN(), A: 2}}}})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		leafVal := fieldMap(v)["mid"].Fields[0].Val.Items[0]
		wantOrder := []string{"z", "bad", "a"}
		for i, want := range wantOrder {
			if leafVal.Fields[i].Key != want {
				t.Errorf("deep field[%d] = %q, want %q", i, leafVal.Fields[i].Key, want)
			}
		}
	})
}

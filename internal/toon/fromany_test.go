package toon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func buildMapAscending() map[string]any {
	m := map[string]any{}
	m["alpha"] = 1
	m["mango"] = "fruit"
	m["zebra"] = true
	m["delta"] = 2.5
	return m
}

func buildMapDescending() map[string]any {
	m := map[string]any{}
	m["delta"] = 2.5
	m["zebra"] = true
	m["mango"] = "fruit"
	m["alpha"] = 1
	return m
}

func TestFromAnyEncodeIsDeterministicAcrossInsertionOrder(t *testing.T) {
	v1, err := FromAny(buildMapAscending())
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	v2, err := FromAny(buildMapDescending())
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}

	out1, err := Encode(v1)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	out2, err := Encode(v2)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if out1 != out2 {
		t.Fatalf("Encode outputs differ across insertion order:\n%q\n%q", out1, out2)
	}

	out1Again, err := Encode(v1)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if out1 != out1Again {
		t.Fatalf("Encode is not stable across repeated calls:\n%q\n%q", out1, out1Again)
	}
}

func TestFromAnyMapKeysAreSorted(t *testing.T) {
	v, err := FromAny(buildMapAscending())
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	want := []string{"alpha", "delta", "mango", "zebra"}
	if len(v.Fields) != len(want) {
		t.Fatalf("got %d fields, want %d", len(v.Fields), len(want))
	}
	for i, k := range want {
		if v.Fields[i].Key != k {
			t.Errorf("field %d key = %q, want %q", i, v.Fields[i].Key, k)
		}
	}
}

func TestFromAnyStructFieldOrderIsPreserved(t *testing.T) {
	type point struct {
		Y int `json:"y"`
		X int `json:"x"`
	}
	v, err := FromAny(point{Y: 2, X: 1})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	if len(v.Fields) != 2 || v.Fields[0].Key != "y" || v.Fields[1].Key != "x" {
		t.Fatalf("field order = %+v, want [y x] (struct declaration order)", v.Fields)
	}
}

func TestFromAnyNormalizesBareNonFiniteFloats(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := FromAny(tc.v)
			if err != nil {
				t.Fatalf("FromAny unexpected error: %v", err)
			}
			if v.Kind != KindNull {
				t.Errorf("Kind = %v, want KindNull", v.Kind)
			}
		})
	}
}

func TestFromAnyNormalizesNestedNonFiniteFloats(t *testing.T) {
	t.Run("in map value", func(t *testing.T) {
		v, err := FromAny(map[string]any{"good": 1.0, "bad": math.NaN()})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		got := fieldMap(v)
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
		if got["good"].Kind != KindNumber || got["good"].Num != "1" {
			t.Errorf("good = %+v, want number 1", got["good"])
		}
	})
	t.Run("in slice element", func(t *testing.T) {
		v, err := FromAny([]float64{1, math.Inf(1), 2})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		if len(v.Items) != 3 {
			t.Fatalf("got %d items, want 3", len(v.Items))
		}
		if v.Items[1].Kind != KindNull {
			t.Errorf("Items[1] = %+v, want KindNull", v.Items[1])
		}
		if v.Items[0].Num != "1" || v.Items[2].Num != "2" {
			t.Errorf("siblings = %+v, %+v, want 1, 2", v.Items[0], v.Items[2])
		}
	})
}

func TestFromAnyStructWithNaNErrors(t *testing.T) {
	type withFloat struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}
	_, err := FromAny(withFloat{A: math.NaN(), B: 3})
	if err == nil {
		t.Fatal("FromAny expected error for struct containing NaN, got nil")
	}
	var uve *json.UnsupportedValueError
	if !errors.As(err, &uve) {
		t.Errorf("expected json.UnsupportedValueError, got %T: %v", err, err)
	}
}

func TestFromAnyMapNaNAndLargeIntSurviveDigitExact(t *testing.T) {
	const bigID = int64(9007199254740993) // > 2^53, would corrupt via float64 round-trip
	v, err := FromAny(map[string]any{"nan": math.NaN(), "id": bigID})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["nan"].Kind != KindNull {
		t.Errorf("nan = %+v, want KindNull", got["nan"])
	}
	if got["id"].Kind != KindNumber || got["id"].Num != "9007199254740993" {
		t.Errorf("id = %+v, want number 9007199254740993 digit-exact", got["id"])
	}
}

func TestFromAnyLeavesByteSliceUntouchedWhenNormalizingSiblingNaN(t *testing.T) {
	v, err := FromAny(map[string]any{"blob": []byte("hi"), "score": math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["score"].Kind != KindNull {
		t.Errorf("score = %+v, want KindNull", got["score"])
	}
	want := base64.StdEncoding.EncodeToString([]byte("hi"))
	if got["blob"].Kind != KindString || got["blob"].Str != want {
		t.Errorf("blob = %+v, want base64 string %q", got["blob"], want)
	}
}

func TestFromAnyChannelStillErrorsAlongsideNonFiniteFloat(t *testing.T) {
	type mixed struct {
		F float64
		C chan int
	}
	if _, err := FromAny(mixed{F: math.NaN(), C: make(chan int)}); err == nil {
		t.Error("FromAny expected error for channel field, got nil")
	}
}

func TestFromAnyNormalizationDoesNotMutateInput(t *testing.T) {
	m := map[string]any{"bad": math.NaN()}
	if _, err := FromAny(m); err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	if !math.IsNaN(m["bad"].(float64)) {
		t.Errorf("original map mutated: m[%q] = %v", "bad", m["bad"])
	}

	s := []float64{math.NaN(), 1}
	if _, err := FromAny(s); err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	if !math.IsNaN(s[0]) {
		t.Errorf("original slice mutated: s[0] = %v", s[0])
	}
}

func TestFromAnyUnsupportedValueErrors(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"channel", make(chan int)},
		{"function", func() {}},
		{"map with non-string non-int key", map[bool]int{true: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FromAny(tc.v); err == nil {
				t.Errorf("FromAny(%s) expected error, got nil", tc.name)
			}
		})
	}
}

func fieldMap(v Value) map[string]Value {
	m := make(map[string]Value, len(v.Fields))
	for _, f := range v.Fields {
		m[f.Key] = f.Val
	}
	return m
}

package toon

import (
	"encoding/base64"
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
	type withFloat struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}
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
	t.Run("in struct field", func(t *testing.T) {
		v, err := FromAny(withFloat{A: math.NaN(), B: 3})
		if err != nil {
			t.Fatalf("FromAny unexpected error: %v", err)
		}
		got := fieldMap(v)
		if got["a"].Kind != KindNull {
			t.Errorf("a = %+v, want KindNull", got["a"])
		}
		if got["b"].Kind != KindNumber || got["b"].Num != "3" {
			t.Errorf("b = %+v, want number 3", got["b"])
		}
	})
}

func TestFromAnyLeavesByteSliceUntouchedWhenNormalizingSiblingNaN(t *testing.T) {
	type withBytes struct {
		Blob  []byte  `json:"blob"`
		Score float64 `json:"score"`
	}
	v, err := FromAny(withBytes{Blob: []byte("hi"), Score: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	got := fieldMap(v)
	if got["score"].Kind != KindNull {
		t.Errorf("score = %+v, want KindNull", got["score"])
	}
	want := base64.StdEncoding.EncodeToString([]byte("hi"))
	if got["blob"].Kind != KindString || got["blob"].Str != want {
		t.Errorf("blob = %+v, want string %q", got["blob"], want)
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

func TestFromAnyEncodeRendersNullForNonFiniteFloat(t *testing.T) {
	type payload struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}
	v, err := FromAny(payload{Name: "x", Score: math.NaN()})
	if err != nil {
		t.Fatalf("FromAny unexpected error: %v", err)
	}
	out, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	want := "name: x\nscore: null"
	if out != want {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}

func fieldMap(v Value) map[string]Value {
	m := make(map[string]Value, len(v.Fields))
	for _, f := range v.Fields {
		m[f.Key] = f.Val
	}
	return m
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

func TestFromAnyNormalizeStructSemantics(t *testing.T) {
	t.Run("omitempty drops zero field alongside NaN", func(t *testing.T) {
		type payload struct {
			Empty string  `json:"empty,omitempty"`
			Bad   float64 `json:"bad"`
		}
		v, err := FromAny(payload{Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got, err := Encode(v)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if got != "bad: null" {
			t.Errorf("got %q, want %q", got, "bad: null")
		}
	})

	t.Run("json:- field excluded", func(t *testing.T) {
		type payload struct {
			Secret string  `json:"-"`
			Bad    float64 `json:"bad"`
		}
		v, err := FromAny(payload{Secret: "hidden", Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)
		if _, present := got["-"]; present {
			t.Error("json:\"-\" field appeared in output")
		}
		if _, present := got["Secret"]; present {
			t.Error("field with json:\"-\" appeared under Go name")
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run("embedded struct promoted flat", func(t *testing.T) {
		type inner struct {
			X int `json:"x"`
		}
		type outer struct {
			inner
			Bad float64 `json:"bad"`
		}
		// Finite twin: what encoding/json would produce for the same struct with Bad=1.
		twin, err := FromAny(outer{inner: inner{X: 42}, Bad: 1})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{inner: inner{X: 42}, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		// Same field set as the finite twin.
		if len(got) != len(twinKeys) {
			t.Errorf("got %d fields, twin has %d", len(got), len(twinKeys))
		}
		if got["x"].Kind != KindNumber || got["x"].Num != "42" {
			t.Errorf("x = %+v, want number 42", got["x"])
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
		if _, present := got["inner"]; present {
			t.Error("anonymous field appeared as key \"inner\" — expected promotion")
		}
	})

	t.Run("NaN with omitempty present as null", func(t *testing.T) {
		type payload struct {
			Score float64 `json:"score,omitempty"`
		}
		v, err := FromAny(payload{Score: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)
		if got["score"].Kind != KindNull {
			t.Errorf("score = %+v, want KindNull (NaN is not the zero value)", got["score"])
		}
	})

	t.Run("nil embedded pointer promoted fields absent no panic", func(t *testing.T) {
		type inner struct {
			X int `json:"x"`
		}
		type outer struct {
			*inner
			Bad float64 `json:"bad"`
		}
		v, err := FromAny(outer{inner: nil, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)
		if _, present := got["x"]; present {
			t.Error("promoted field x should be absent when embedded pointer is nil")
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run("non-nil empty slice with omitempty omitted matches finite twin", func(t *testing.T) {
		type payload struct {
			S   []string `json:"s,omitempty"`
			Bad float64  `json:"bad"`
		}
		// Finite twin via fast path: encoding/json omits a non-nil empty slice.
		twin, err := FromAny(payload{S: []string{}, Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(payload{S: []string{}, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v — field sets must match", keySet(got), keySet(twinKeys))
		}
		if _, present := got["s"]; present {
			t.Error("empty slice with omitempty should be omitted, matching encoding/json")
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run("zero struct field with omitempty kept matches finite twin", func(t *testing.T) {
		type inner struct {
			A int `json:"a"`
		}
		type payload struct {
			In  inner   `json:"in,omitempty"`
			Bad float64 `json:"bad"`
		}
		// Finite twin via fast path: encoding/json keeps a zero struct even with omitempty.
		twin, err := FromAny(payload{In: inner{A: 0}, Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(payload{In: inner{A: 0}, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v — field sets must match", keySet(got), keySet(twinKeys))
		}
		if _, present := got["in"]; !present {
			t.Error("zero struct with omitempty should be kept, matching encoding/json")
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run("tagged anonymous field treated as named not promoted matches finite twin", func(t *testing.T) {
		type inner struct {
			A int `json:"a"`
		}
		type outer struct {
			inner `json:"in"`
			Bad   float64 `json:"bad"`
		}
		// Finite twin via fast path: encoding/json emits inner as {"in":{"a":5}}.
		twin, err := FromAny(outer{inner: inner{A: 5}, Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{inner: inner{A: 5}, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v — field sets must match", keySet(got), keySet(twinKeys))
		}
		if _, present := got["in"]; !present {
			t.Error("tagged anonymous field should appear as named key \"in\"")
		}
		if _, present := got["a"]; present {
			t.Error("promoted field \"a\" must not appear — only nested under \"in\"")
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run(",string option wraps finite value as json string", func(t *testing.T) {
		type payload struct {
			N   int     `json:"n,string"`
			Bad float64 `json:"bad"`
		}
		// Finite twin via fast path: encoding/json wraps N in a JSON string.
		twin, err := FromAny(payload{N: 42, Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinGot := fieldMap(twin)

		v, err := FromAny(payload{N: 42, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if twinGot["n"].Kind != KindString || twinGot["n"].Str != "42" {
			t.Errorf("twin n = %+v, want string \"42\"", twinGot["n"])
		}
		if got["n"].Kind != KindString || got["n"].Str != "42" {
			t.Errorf("n = %+v, want string \"42\" (,string option must pass through)", got["n"])
		}
		if got["bad"].Kind != KindNull {
			t.Errorf("bad = %+v, want KindNull", got["bad"])
		}
	})

	t.Run("cross-depth same json name shallower tagged wins matches finite twin", func(t *testing.T) {
		type innerX struct{ X string `json:"x"` }
		type outer struct {
			innerX
			A   string  `json:"x"`
			Bad float64 `json:"bad"`
		}
		twin, err := FromAny(outer{innerX: innerX{X: "deep"}, A: "shallow", Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{innerX: innerX{X: "deep"}, A: "shallow", Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v", keySet(got), keySet(twinKeys))
		}
		if got["x"].Kind != KindString || got["x"].Str != "shallow" {
			t.Errorf("x = %+v, want \"shallow\" (shallower field wins)", got["x"])
		}
	})

	t.Run("same-depth json name collision both dropped matches finite twin", func(t *testing.T) {
		type outer struct {
			A   string  `json:"dup"`
			B   string  `json:"dup"` //nolint:govet
			Bad float64 `json:"bad"`
		}
		twin, err := FromAny(outer{A: "a", B: "b", Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{A: "a", B: "b", Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v", keySet(got), keySet(twinKeys))
		}
		if _, present := got["dup"]; present {
			t.Error("same-depth collision: both fields must be dropped, matching encoding/json")
		}
	})

	t.Run("struct whose json form is {} with omitempty kept matches finite twin", func(t *testing.T) {
		type emptyInner struct {
			Hidden string `json:"h,omitempty"`
		}
		type outer struct {
			In  emptyInner `json:"in,omitempty"`
			Bad float64    `json:"bad"`
		}
		twin, err := FromAny(outer{Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v", keySet(got), keySet(twinKeys))
		}
		if _, present := got["in"]; !present {
			t.Error("struct whose json form is {} must not be omitted by omitempty, matching encoding/json")
		}
	})

	t.Run("promoted field alone with no shallower conflict survives matches finite twin", func(t *testing.T) {
		type innerX struct{ X string `json:"x"` }
		type outer struct {
			innerX
			Bad float64 `json:"bad"`
		}
		twin, err := FromAny(outer{innerX: innerX{X: "deep"}, Bad: 1.0})
		if err != nil {
			t.Fatalf("FromAny twin error: %v", err)
		}
		twinKeys := fieldMap(twin)

		v, err := FromAny(outer{innerX: innerX{X: "deep"}, Bad: math.NaN()})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)

		if len(got) != len(twinKeys) {
			t.Errorf("got keys %v, twin has %v", keySet(got), keySet(twinKeys))
		}
		if got["x"].Kind != KindString || got["x"].Str != "deep" {
			t.Errorf("x = %+v, want \"deep\" (promoted field with no conflict)", got["x"])
		}
	})

	t.Run("pointer to NaN with omitempty kept as null", func(t *testing.T) {
		type payload struct {
			P *float64 `json:"p,omitempty"`
			B float64  `json:"b"`
		}
		nan := math.NaN()
		v, err := FromAny(payload{P: &nan, B: 1})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)
		p, present := got["p"]
		if !present {
			t.Fatal("p omitted; a non-nil pointer is never empty for omitempty")
		}
		if p.Kind != KindNull {
			t.Errorf("p = %+v, want KindNull", p)
		}
	})

	t.Run("any holding NaN with omitempty kept as null", func(t *testing.T) {
		type payload struct {
			V any     `json:"v,omitempty"`
			B float64 `json:"b"`
		}
		v, err := FromAny(payload{V: math.NaN(), B: 1})
		if err != nil {
			t.Fatalf("FromAny error: %v", err)
		}
		got := fieldMap(v)
		val, present := got["v"]
		if !present {
			t.Fatal("v omitted; a non-nil interface is never empty for omitempty")
		}
		if val.Kind != KindNull {
			t.Errorf("v = %+v, want KindNull", val)
		}
	})
}

func keySet(m map[string]Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

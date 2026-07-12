package toon

import "testing"

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

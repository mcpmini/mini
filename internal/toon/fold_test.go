package toon

import (
	"encoding/json"
	"testing"
)

func encodeJSON(t *testing.T, raw string) string {
	t.Helper()
	v, err := FromJSON(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	return got
}

func TestFoldSafeSingleKeyChains(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"chain to primitive", `{"a":{"b":{"c":1}}}`, "a.b.c: 1"},
		{"chain to inline array", `{"data":{"meta":{"items":["x","y"]}}}`, "data.meta.items[2]: x,y"},
		{"chain to empty object", `{"a":{"b":{"c":{}}}}`, "a.b.c:"},
		{"chain to empty array", `{"a":{"b":[]}}`, "a.b: []"},
		{"two-segment chain", `{"user":{"login":"x"}}`, "user.login: x"},
		{
			"chain to tabular array",
			`{"a":{"b":{"items":[{"id":1,"name":"A"},{"id":2,"name":"B"}]}}}`,
			"a.b.items[2]{id,name}:\n  1,A\n  2,B",
		},
		{
			"sibling order preserved across folds",
			`{"first":{"second":{"third":1}},"simple":2,"short":{"path":3}}`,
			"first.second.third: 1\nsimple: 2\nshort.path: 3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeJSON(t, tc.raw); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldSkipsUnsafeChains(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"sibling literal key collision keeps whole chain nested",
			`{"data":{"meta":{"items":[1,2]}},"data.meta.items":"literal"}`,
			"data:\n  meta:\n    items[2]: 1,2\ndata.meta.items: literal",
		},
		{
			"segment failing identifier rule",
			`{"data":{"full-name":{"x":1}}}`,
			"data:\n  \"full-name\":\n    x: 1",
		},
		{
			"dotted segment fails identifier rule",
			`{"a":{"b.c":1}}`,
			"a:\n  b.c: 1",
		},
		{
			"multi-key object terminates chain unfolded",
			`{"a":{"b":{"x":1,"y":2}}}`,
			"a:\n  b:\n    x: 1\n    y: 2",
		},
		{
			"nothing folds along the failed chain spine",
			`{"my-key":{"a":{"b":{"x":1,"y":2}}}}`,
			"\"my-key\":\n  a:\n    b:\n      x: 1\n      y: 2",
		},
		{
			"fresh chains fold beyond a failed chain",
			`{"my-key":{"a":{"x":{"p":1},"y":2}}}`,
			"\"my-key\":\n  a:\n    x.p: 1\n    y: 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeJSON(t, tc.raw); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldStopsAtArrayBoundary(t *testing.T) {
	t.Run("array is the fold leaf", func(t *testing.T) {
		want := "a.b[2]: 1,2"
		if got := encodeJSON(t, `{"a":{"b":[1,2]}}`); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
	t.Run("chain never continues into array elements", func(t *testing.T) {
		want := "a.b[1]{c.d}:\n  1"
		if got := encodeJSON(t, `{"a":{"b":[{"c":{"d":1}}]}}`); got != want {
			t.Errorf("Encode() = %q, want a.b folded, c.d folded inside the element, never a.b.c.d (%q)", got, want)
		}
	})
	t.Run("elements fold independently and become tabular columns", func(t *testing.T) {
		want := "items[2]{user.login}:\n  x\n  y"
		if got := encodeJSON(t, `{"items":[{"user":{"login":"x"}},{"user":{"login":"y"}}]}`); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
}

//go:build test

package jq

import (
	"context"
	"testing"
)

func TestFormatPath(t *testing.T) {
	cases := []struct {
		name string
		path []string
		want string
	}{
		{"empty", nil, ""},
		{"single identifier", []string{"items"}, ".items"},
		{"chained identifiers", []string{"items", "[0]", "body"}, ".items[0].body"},
		{"array index first", []string{"[0]", "body"}, ".[0].body"},
		{"consecutive array indices", []string{"[0]", "[1]", "body"}, ".[0][1].body"},
		{"non-identifier key", []string{"foo bar"}, `.["foo bar"]`},
		{"non-identifier key first", []string{"foo bar", "baz"}, `.["foo bar"].baz`},
		{"leading digit key", []string{"1leading"}, `.["1leading"]`},
		{"dash key", []string{"has-dash"}, `.["has-dash"]`},
		{"backslash key", []string{`a\b`}, `.["a\\b"]`},
		{"quoted key", []string{`say "hi"`}, `.["say \"hi\""]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatPath(tc.path)
			if got != tc.want {
				t.Errorf("FormatPath(%v) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestFormatPath_roundTripViaEval(t *testing.T) {
	cases := []struct {
		name  string
		path  []string
		input string
		want  string
	}{
		{"root array index", []string{"[0]", "body"}, `[{"body":"ok"}]`, `"ok"`},
		{"nested array index", []string{"items", "[0]", "body"}, `{"items":[{"body":"ok"}]}`, `"ok"`},
		{"bracket key", []string{"body text"}, `{"body text":"ok"}`, `"ok"`},
		{"backslash key", []string{`a\b`}, `{"a\\b":"ok"}`, `"ok"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			filter := FormatPath(c.path)
			got, err := Eval(context.Background(), []byte(c.input), filter)
			if err != nil {
				t.Fatalf("Eval(%q, %q): %v", c.input, filter, err)
			}
			if got != c.want {
				t.Errorf("Eval(%q, %q) = %q, want %q", c.input, filter, got, c.want)
			}
		})
	}
}

func TestCollapseIndex(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// no-ops
		{"empty", "", ""},
		{"no brackets", ".foo", ".foo"},
		{"already wildcard", ".items[]", ".items[]"},
		{"already wildcard root", ".[]", ".[]"},

		// basic index collapsing
		{"root array index", ".[0]", ".[]"},
		{"field then index", ".items[0]", ".items[]"},
		{"multi-digit index", ".items[42]", ".items[]"},
		{"large index", ".items[9999]", ".items[]"},

		// index followed by a field
		{"root index then field", ".[0].body", ".[].body"},
		{"field index field", ".items[0].name", ".items[].name"},

		// nested arrays
		{"nested arrays", ".[0][1]", ".[][]"},
		{"field nested arrays", ".matrix[0][1]", ".matrix[][]"},
		{"multi-level", ".a[0].b[1].c[2]", ".a[].b[].c[]"},

		// quoted keys — [N] inside must NOT be replaced
		{"quoted key plain", `.["foo"]`, `.["foo"]`},
		{"quoted key with numeric segment", `.["foo[0]bar"]`, `.["foo[0]bar"]`},
		{"quoted key numeric only content", `.["[0]"]`, `.["[0]"]`},
		{"quoted key then index", `.[" foo"][0]`, `.[" foo"][]`},
		{"index then quoted key", `.[0]["foo"]`, `.[]["foo"]`},
		{"quoted key with escaped quote", `.["foo\"bar"]`, `.["foo\"bar"]`},
		{"quoted key escaped quote then index", `.["a\"b"][0]`, `.["a\"b"][]`},

		// non-numeric bracket content (not touched)
		{"bracket with letters", ".items[abc]", ".items[abc]"},
		{"bracket with mixed", ".items[0abc]", ".items[0abc]"},
		{"negative index", ".items[-1]", ".items[-1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CollapseIndex(tc.in)
			if got != tc.want {
				t.Errorf("CollapseIndex(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

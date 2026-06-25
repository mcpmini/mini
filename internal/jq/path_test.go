package jq

import "testing"

func TestFormatPath(t *testing.T) {
	cases := []struct {
		name string
		path []string
		want string
	}{
		{"empty", nil, ""},
		{"single identifier", []string{"foo"}, ".foo"},
		{"chained identifiers", []string{"foo", "bar"}, ".foo.bar"},
		{"field then array index", []string{"items", "[0]"}, ".items[0]"},
		{"array index first", []string{"[0]", "foo"}, ".[0].foo"},
		{"non-identifier key", []string{"foo bar"}, `.["foo bar"]`},
		{"non-identifier key first", []string{"foo bar", "baz"}, `.["foo bar"].baz`},
		{"key with quotes", []string{`fo"o`}, `.["fo\"o"]`},
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

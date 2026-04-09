//go:build test

package main

import "testing"

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"foo", "'foo'"},
		{"", "''"},
		{"it's alive", "'it'\\''s alive'"},
		{"a'b'c", "'a'\\''b'\\''c'"},
		{"no special", "'no special'"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := shellQuote(tc.in)
			if got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

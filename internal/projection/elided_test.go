package projection

import (
	"slices"
	"testing"
)

func TestCollapseElided_deduplicatesArrayIndices(t *testing.T) {
	paths := []string{".items[0].secret", ".items[1].secret", ".items[2].secret"}
	got := collapseElided(paths)
	want := []string{".items[].secret"}
	if !slices.Equal(got, want) {
		t.Fatalf("collapseElided() = %#v, want %#v", got, want)
	}
}

func TestCollapseElided_keepsDistinctPaths(t *testing.T) {
	paths := []string{".items[0].secret", ".items[0].other", ".meta"}
	got := collapseElided(paths)
	slices.Sort(got)
	want := []string{".items[].other", ".items[].secret", ".meta"}
	if !slices.Equal(got, want) {
		t.Fatalf("collapseElided() = %#v, want %#v", got, want)
	}
}

func TestCollapseElided_emptyInput(t *testing.T) {
	if got := collapseElided(nil); got != nil {
		t.Fatalf("collapseElided(nil) = %v, want nil", got)
	}
}

func TestIndexedToWildcard_replacesNumericIndex(t *testing.T) {
	cases := []struct{ in, want string }{
		{".items[0].body", ".items[].body"},
		{".items[42].patch", ".items[].patch"},
		{"[0]", "[]"},
		{".no_index", ".no_index"},
		{".items[].body", ".items[].body"}, // already wildcard
	}
	for _, tc := range cases {
		got := indexedToWildcard(tc.in)
		if got != tc.want {
			t.Errorf("indexedToWildcard(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsExcludedSupportsDotAndArrayNotation(t *testing.T) {
	exclude := []string{"env", "pipeline.configuration", "steps[].agent"}
	cases := []struct {
		key  string
		want bool
	}{
		{key: "env", want: true},
		{key: "pipeline", want: true},
		{key: "steps", want: true},
		{key: "other", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := isExcluded(tc.key, exclude); got != tc.want {
				t.Fatalf("isExcluded(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

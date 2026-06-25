package projection

import (
	"slices"
	"testing"
)

func TestCollapseElided_deduplicatesArrayIndices(t *testing.T) {
	paths := []string{".items[0].secret", ".items[1].secret", ".items[2].secret"}
	got := collapseExcluded(paths)
	want := []string{".items[].secret"}
	if !slices.Equal(got, want) {
		t.Fatalf("collapseExcluded() = %#v, want %#v", got, want)
	}
}

func TestCollapseElided_keepsDistinctPaths(t *testing.T) {
	paths := []string{".items[0].secret", ".items[0].other", ".meta"}
	got := collapseExcluded(paths)
	slices.Sort(got)
	want := []string{".items[].other", ".items[].secret", ".meta"}
	if !slices.Equal(got, want) {
		t.Fatalf("collapseExcluded() = %#v, want %#v", got, want)
	}
}

func TestCollapseElided_emptyInput(t *testing.T) {
	if got := collapseExcluded(nil); got != nil {
		t.Fatalf("collapseExcluded(nil) = %v, want nil", got)
	}
}

func TestIsExcludedSupportsDotAndArrayNotation(t *testing.T) {
	exclude := []string{"env", "pipeline.configuration", "steps[].agent", "files[]"}
	cases := []struct {
		key  string
		want bool
	}{
		{key: "env", want: true},
		{key: "pipeline", want: true},
		{key: "steps", want: true},
		{key: "files", want: true},
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

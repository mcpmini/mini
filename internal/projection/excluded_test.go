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
	want := []string{".items[].other", ".items[].secret", ".meta"}
	if !slices.Equal(got, want) {
		t.Fatalf("collapseExcluded() = %#v, want %#v", got, want)
	}
}

func TestCollapseElided_outputIsSorted(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{
			name:  "reverse input order",
			paths: []string{".z", ".a", ".m"},
			want:  []string{".a", ".m", ".z"},
		},
		{
			name:  "collapsed array indices sort after collapse",
			paths: []string{".items[1].name", ".items[0].name", ".items[2].age", ".items[0].age"},
			want:  []string{".items[].age", ".items[].name"},
		},
		{
			name:  "mixed plain and array paths",
			paths: []string{".z[0].x", ".a", ".z[1].x", ".b"},
			want:  []string{".a", ".b", ".z[].x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collapseExcluded(tc.paths)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("collapseExcluded(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
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

package projection

import (
	"slices"
	"testing"
)

func TestCollectElidedReportsNestedKeys(t *testing.T) {
	original := map[string]any{
		"status": "ok",
		"env": map[string]any{
			"secret": "x",
			"safe":   "y",
		},
		"meta": map[string]any{
			"trace": "abc",
		},
	}
	projected := map[string]any{
		"status": "ok",
		"env": map[string]any{
			"safe": "y",
		},
	}
	got := collectElided(original, projected, "")
	slices.Sort(got)
	want := []string{"env.secret", "meta"}
	if !slices.Equal(got, want) {
		t.Fatalf("collectElided() = %#v, want %#v", got, want)
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

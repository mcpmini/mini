package projection_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
)

func TestExcludeRootRelativeObjectPaths(t *testing.T) {
	cases := []struct {
		name, input, want string
		exclude, removed  []string
	}{
		{
			name:    "bare name preserves nested descriptions",
			input:   `{"number":42,"body":"fix","description":"root","repo":{"description":"nested"}}`,
			want:    `{"number":42,"body":"fix","repo":{"description":"nested"}}`,
			exclude: []string{"description"}, removed: []string{".description"},
		},
		{
			name:    "dotted path removes only leaf",
			input:   `{"head":{"ref":"feature","sha":"abc","repo":{"id":1,"description":"head"}},"base":{"repo":{"description":"base"}}}`,
			want:    `{"head":{"ref":"feature","sha":"abc","repo":{"id":1}},"base":{"repo":{"description":"base"}}}`,
			exclude: []string{"head.repo.description"},
			removed: []string{".head.repo.description"},
		},
		{
			name: "root array elements are untouched", input: `[{"description":"keep","repo":{"description":"keep"}}]`,
			want:    `[{"description":"keep","repo":{"description":"keep"}}]`,
			exclude: []string{"description", "repo.description", "0.description"},
		},
		{
			name:    "nested array elements are untouched",
			input:   `{"items":[{"description":"keep","repo":{"description":"keep"}}]}`,
			want:    `{"items":[{"description":"keep","repo":{"description":"keep"}}]}`,
			exclude: []string{"description", "items.description", "items.0.description"},
		},
		{
			name: "whole array fields can be removed", input: `{"items":[1],"data":{"items":[2],"id":3},"id":4}`,
			want: `{"data":{"id":3},"id":4}`, exclude: []string{"items", "data.items"},
			removed: []string{".data.items", ".items"},
		},
		{
			name: "missing and scalar paths are no ops", input: `{"head":"scalar","id":4}`,
			want: `{"head":"scalar","id":4}`, exclude: []string{"head.repo.description", "missing.id"},
		},
		{
			name:    "literal dots are not path separators",
			input:   `{"head.repo":{"description":"literal"},"head":{"repo":{"description":"nested","id":1}}}`,
			want:    `{"head.repo":{"description":"literal"},"head":{"repo":{"id":1}}}`,
			exclude: []string{"head.repo.description"},
			removed: []string{".head.repo.description"},
		},
		{
			name:    "numeric object fields are supported",
			input:   `{"0":{"description":"remove"},"items":[{"description":"keep"}]}`,
			want:    `{"0":{},"items":[{"description":"keep"}]}`,
			exclude: []string{"0.description"},
			removed: []string{`.["0"].description`},
		},
		{
			name:    "invalid typed rules cannot delete ancestors",
			input:   `{"items":[{"description":"keep"}],"head":{"description":"keep"}}`,
			want:    `{"items":[{"description":"keep"}],"head":{"description":"keep"}}`,
			exclude: []string{"items[].description", "items[*].description", "head.", "head..description"},
		},
		{name: "nil exclusions", input: `{"description":"keep"}`, want: `{"description":"keep"}`},
		{
			name:    "empty exclusions",
			input:   `{"description":"keep"}`,
			want:    `{"description":"keep"}`,
			exclude: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := exclusionJSON(t, tc.input)
			result := projection.Apply(input, &config.ProjectionConfig{Exclude: tc.exclude}, &projection.Defaults{})
			if want := exclusionJSON(t, tc.want); !reflect.DeepEqual(result.Summary, want) {
				t.Errorf("projected value = %#v, want %#v", result.Summary, want)
			}
			if !slices.Equal(result.ExcludedKeys, tc.removed) {
				t.Errorf("excluded paths = %v, want %v", result.ExcludedKeys, tc.removed)
			}
			if !reflect.DeepEqual(input, exclusionJSON(t, tc.input)) {
				t.Error("Apply mutated its input")
			}
		})
	}
}

func exclusionJSON(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

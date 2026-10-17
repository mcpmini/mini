package projection

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
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
			result := Apply(input, &config.ProjectionConfig{Exclude: tc.exclude}, &Defaults{})
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

func TestExcludedKeys_sortedDeterministic(t *testing.T) {
	cfg := &config.ProjectionConfig{Exclude: []string{"zebra", "apple", "mango"}}
	value := map[string]any{"zebra": 1, "apple": 2, "mango": 3, "keep": 4}
	result := Apply(value, cfg, &Defaults{})
	want := []string{".apple", ".mango", ".zebra"}
	if !slices.Equal(result.ExcludedKeys, want) {
		t.Errorf("ExcludedKeys = %v, want %v (sorted)", result.ExcludedKeys, want)
	}
}

func TestExcludedKeys(t *testing.T) {
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"status"}}
	value := map[string]any{
		"status": "ok",
		"env":    "secret",
		"body":   "long text",
	}

	result := Apply(value, cfg, &Defaults{})
	want := []string{".body", ".env"}
	if !slices.Equal(result.ExcludedKeys, want) {
		t.Errorf("ExcludedKeys = %v, want %v", result.ExcludedKeys, want)
	}
}

func TestIncludeFilter(t *testing.T) {
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"status", "branch"}}
	value := map[string]any{
		"status": "failed",
		"branch": "main",
		"env":    map[string]any{"SECRET": "password"},
	}

	result := Apply(value, cfg, &Defaults{})
	m := result.Summary.(map[string]any)

	if _, ok := m["env"]; ok {
		t.Error("env should be excluded by include filter")
	}
	if m["status"] != "failed" {
		t.Errorf("expected status=failed, got %v", m["status"])
	}
}

func TestIncludeFilterOnlyTopLevel_excludesNonListed(t *testing.T) {
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"issues", "totalCount"}}
	value := map[string]any{
		"issues":     []any{map[string]any{"number": 1, "title": "Bug report", "state": "open"}},
		"totalCount": 42,
		"pageInfo":   map[string]any{"hasNextPage": true},
	}
	result := Apply(value, cfg, &Defaults{})
	m := result.Summary.(map[string]any)
	if _, ok := m["pageInfo"]; ok {
		t.Error("pageInfo should be excluded by include filter")
	}
	if m["totalCount"] == nil {
		t.Error("totalCount should be included")
	}
}

func TestIncludeFilterOnlyTopLevel_preservesNestedFields(t *testing.T) {
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"issues"}}
	value := map[string]any{
		"issues": []any{map[string]any{"number": 1, "title": "Bug report", "state": "open"}},
	}
	result := Apply(value, cfg, &Defaults{})
	m := result.Summary.(map[string]any)
	issues, ok := m["issues"].([]any)
	if !ok || len(issues) == 0 {
		t.Fatal("issues array should be present and non-empty")
	}
	issue := issues[0].(map[string]any)
	for _, key := range []string{"number", "title", "state"} {
		if issue[key] == nil {
			t.Errorf("%s inside issue should not be stripped by include filter", key)
		}
	}
}

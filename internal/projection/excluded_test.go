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
			name:    "rules without [] do not cross a root array",
			input:   `[{"description":"keep","repo":{"description":"keep"}}]`,
			want:    `[{"description":"keep","repo":{"description":"keep"}}]`,
			exclude: []string{"description", "repo.description", "0.description"},
		},
		{
			name:    "rules without [] do not cross a nested array",
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
			name:    "invalid typed rules are no ops",
			input:   `{"items":[{"description":"keep"}],"head":{"description":"keep"}}`,
			want:    `{"items":[{"description":"keep"}],"head":{"description":"keep"}}`,
			exclude: []string{"items.[0].description", "items[0].description", "items[]", "head.", "head..description"},
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

func TestExcludeArrayItemFields(t *testing.T) {
	cases := []struct {
		name, input, want string
		exclude, removed  []string
	}{
		{
			name:    "root array entries keep everything else",
			input:   `[{"sha":"a","author":{"login":"x","avatar_url":"u"},"committer":{"avatar_url":"c"}},{"sha":"b","author":null},{"sha":"c"},{"sha":"d","author":{"login":"y"}},7,"s",null]`,
			want:    `[{"sha":"a","author":{"login":"x"},"committer":{"avatar_url":"c"}},{"sha":"b","author":null},{"sha":"c"},{"sha":"d","author":{"login":"y"}},7,"s",null]`,
			exclude: []string{"[].author.avatar_url"},
			removed: []string{".[].author.avatar_url"},
		},
		{
			name:    "wrapped array entries",
			input:   `{"total":2,"items":[{"id":1,"secret":"x"},{"id":2,"secret":"y"}],"secret":"root"}`,
			want:    `{"total":2,"items":[{"id":1},{"id":2}],"secret":"root"}`,
			exclude: []string{"items[].secret"},
			removed: []string{".items[].secret"},
		},
		{
			name:    "nested arrays skip mismatched branches",
			input:   `{"groups":[{"members":[{"id":1,"avatar_url":"a"},[{"avatar_url":"nested"}]]},{"members":{"avatar_url":"object"}},{"members":null},{"members":[]},"scalar"]}`,
			want:    `{"groups":[{"members":[{"id":1},[{"avatar_url":"nested"}]]},{"members":{"avatar_url":"object"}},{"members":null},{"members":[]},"scalar"]}`,
			exclude: []string{"groups[].members[].avatar_url"},
			removed: []string{".groups[].members[].avatar_url"},
		},
		{
			name:    "repeated array steps",
			input:   `{"matrix":[[{"secret":1,"id":1}],[{"secret":2}],{"secret":"not an array"}]}`,
			want:    `{"matrix":[[{"id":1}],[{}],{"secret":"not an array"}]}`,
			exclude: []string{"matrix[][].secret"},
			removed: []string{".matrix[][].secret"},
		},
		{
			name:    "an object where an array is expected is untouched",
			input:   `{"items":{"secret":"keep","0":{"secret":"keep"}}}`,
			want:    `{"items":{"secret":"keep","0":{"secret":"keep"}}}`,
			exclude: []string{"items[].secret"},
		},
		{
			name:    "object keys spelled like array steps are fields",
			input:   `{"items":{"":{"secret":"keep"},"[]":{"secret":"keep"},"[0]":{"secret":"keep"}},"items[]":{"secret":"keep"}}`,
			want:    `{"items":{"":{"secret":"keep"},"[]":{"secret":"keep"},"[0]":{"secret":"keep"}},"items[]":{"secret":"keep"}}`,
			exclude: []string{"items[].secret"},
		},
		{
			name:    "a whole array field inside entries",
			input:   `[{"id":1,"labels":["a"]},{"id":2}]`,
			want:    `[{"id":1},{"id":2}]`,
			exclude: []string{"[].labels"},
			removed: []string{".[].labels"},
		},
		{
			name:    "overlapping and duplicate rules report the dropped parent once",
			input:   `[{"id":1,"author":{"avatar_url":"u","login":"x"}}]`,
			want:    `[{"id":1}]`,
			exclude: []string{"[].author.avatar_url", "[].author", "[].author"},
			removed: []string{".[].author"},
		},
		{
			name:    "a valid rule with no matches",
			input:   `[{"id":1}]`,
			want:    `[{"id":1}]`,
			exclude: []string{"[].author.avatar_url"},
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

func TestExcludeArrayItemFieldsWithinLimits(t *testing.T) {
	input := `[{"id":1,"secret":"a","author":{"avatar_url":"u"}},{"id":2,"secret":"b"},{"id":3,"secret":"c"}]`
	t.Run("items cut by an array limit are reported as truncation only", func(t *testing.T) {
		cfg := &config.ProjectionConfig{Exclude: []string{"[].secret"}, ArrayLimits: map[string]int{"default": 1}}
		result := Apply(exclusionJSON(t, input), cfg, &Defaults{})
		want := exclusionJSON(t, `[{"id":1,"author":{"avatar_url":"u"}}]`)
		if !reflect.DeepEqual(result.Summary, want) {
			t.Errorf("projected value = %#v, want %#v", result.Summary, want)
		}
		if !slices.Equal(result.ExcludedKeys, []string{".[].secret"}) {
			t.Errorf("excluded paths = %v, want [.[].secret]", result.ExcludedKeys)
		}
		if wantCut := []Truncation{{JQPath: ".", Items: 2}}; !slices.Equal(result.Truncated, wantCut) {
			t.Errorf("truncated = %v, want %v", result.Truncated, wantCut)
		}
	})
	t.Run("fields below the depth limit are not traversed", func(t *testing.T) {
		cfg := &config.ProjectionConfig{Exclude: []string{"[].author.avatar_url"}, DepthLimit: 1}
		result := Apply(exclusionJSON(t, input), cfg, &Defaults{})
		first := result.Summary.([]any)[0].(map[string]any)
		if first["author"] != "[depth limit reached]" || len(result.ExcludedKeys) != 0 {
			t.Errorf(
				"author = %v, excluded = %v; want the depth marker and no exclusions",
				first["author"],
				result.ExcludedKeys,
			)
		}
	})
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

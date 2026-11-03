//go:build test

package server

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

type outputSchemaCase struct {
	name     string
	upstream string
	want     string
}

func TestOutputSchema_DropsKeywordsATrimCanBreak(t *testing.T) {
	runOutputSchemaCases(t, []outputSchemaCase{
		{
			name: "keywords a trim can fail are dropped at every depth, types and enums kept",
			upstream: `{"type":"object","required":["id"],"minProperties":1,"maxProperties":9,` +
				`"dependentRequired":{"a":["b"]},"dependencies":{"a":["b"]},` +
				`"properties":{"id":{"type":"string","pattern":"\\S","minLength":1,"format":"uuid","enum":["x"]},` +
				`"tags":{"type":"array","minItems":1,"maxItems":3,"contains":{"const":"a"},"minContains":1,"uniqueItems":true,` +
				`"items":{"type":"object","required":["k"],"properties":{"k":{"type":"string"}}}}},` +
				`"$defs":{"Ref":{"type":"object","required":["r"]}}}`,
			want: `{"type":"object","properties":{"id":{"type":"string","enum":["x"]},` +
				`"tags":{"type":"array","items":{"type":"object","properties":{"k":{"type":"string"}}}},"__mini":MINI},` +
				`"$defs":{"Ref":{"type":"object"}}}`,
		},
		{
			name: "required in each allOf part is dropped",
			upstream: `{"allOf":[{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]},` +
				`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}]}`,
			want: `{"properties":{"__mini":MINI},"allOf":[` +
				`{"type":"object","properties":{"name":{"type":"string"},"__mini":MINI}},` +
				`{"type":"object","properties":{"count":{"type":"integer"},"__mini":MINI}}]}`,
		},
		{
			name: "unevaluatedItems goes with contains so matched items stay allowed",
			upstream: `{"type":"object","properties":{"matches":{"type":"array","contains":{"type":"integer"},` +
				`"unevaluatedItems":false}}}`,
			want: `{"type":"object","properties":{"matches":{"type":"array"},"__mini":MINI}}`,
		},
		{
			name: "oneOf becomes anyOf since branches without required can overlap",
			upstream: `{"oneOf":[` +
				`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false},` +
				`{"type":"object","properties":{"sha":{"type":"string"}},"required":["sha"],"additionalProperties":false},` +
				`{"type":"null"}]}`,
			want: `{"properties":{"__mini":MINI},"anyOf":[` +
				`{"type":"object","properties":{"title":{"type":"string"},"__mini":MINI},"additionalProperties":false},` +
				`{"type":"object","properties":{"sha":{"type":"string"},"__mini":MINI},"additionalProperties":false},` +
				`{"type":"null"}]}`,
		},
		{
			name:     "oneOf beside anyOf is kept as an anyOf inside allOf",
			upstream: `{"type":"object","anyOf":[{"type":"object"}],"oneOf":[{"type":"object"}]}`,
			want: `{"type":"object","properties":{"__mini":MINI},"anyOf":[{"type":"object","properties":{"__mini":MINI}}],` +
				`"allOf":[{"properties":{"__mini":MINI},"anyOf":[{"type":"object","properties":{"__mini":MINI}}]}]}`,
		},
		{
			name:     "properties named like keywords are kept",
			upstream: `{"type":"object","properties":{"required":{"type":"boolean"},"format":{"type":"string"}}}`,
			want:     `{"type":"object","properties":{"required":{"type":"boolean"},"format":{"type":"string"},"__mini":MINI}}`,
		},
		{
			name: "not and if are untouched because loosening what they test would tighten the schema",
			upstream: `{"type":"object","properties":{"a":{"not":{"required":["x"]}},` +
				`"b":{"if":{"required":["y"]},"then":{"required":["z"]}}}}`,
			want: `{"type":"object","properties":{"a":{"not":{"required":["x"]}},"b":{"if":{"required":["y"]},"then":{}},` +
				`"__mini":MINI}}`,
		},
		{
			name: "branches told apart by a forbidden property keep that rule",
			upstream: `{"oneOf":[{"type":"object","properties":{"labels":{"not":{"description":"Any value."}}},` +
				`"additionalProperties":false}]}`,
			want: `{"properties":{"__mini":MINI},"anyOf":[{"type":"object","properties":{"labels":{"not":{"description":"Any value."}},` +
				`"__mini":MINI},"additionalProperties":false}]}`,
		},
		{
			name:     "declared draft-07 dialect is loosened like 2020-12",
			upstream: `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["a"]}`,
			want:     `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"__mini":MINI}}`,
		},
	})
}

func TestOutputSchema_AllowsTheNoticeWhereCarryPutsIt(t *testing.T) {
	runOutputSchemaCases(t, []outputSchemaCase{
		{
			name:     "closed object gains __mini",
			upstream: `{"type":"object","properties":{"temp":{"type":"number"}},"additionalProperties":false}`,
			want:     `{"type":"object","properties":{"temp":{"type":"number"},"__mini":MINI},"additionalProperties":false}`,
		},
		{
			name:     "properties without type gain __mini",
			upstream: `{"properties":{"temp":{"type":"number"}}}`,
			want:     `{"properties":{"temp":{"type":"number"},"__mini":MINI}}`,
		},
		{
			name:     "object without properties gets them",
			upstream: `{"type":"object"}`,
			want:     `{"type":"object","properties":{"__mini":MINI}}`,
		},
		{
			name:     "an upstream __mini property is kept",
			upstream: `{"type":"object","properties":{"__mini":{"type":"string"}},"required":["__mini"]}`,
			want:     `{"type":"object","properties":{"__mini":{"type":"string"}}}`,
		},
		{
			name:     "list items also allow a trailing notice",
			upstream: `{"type":["null","array"],"items":{"type":"string"},"minItems":1,"maxItems":5}`,
			want:     `{"type":["null","array"],"items":{"anyOf":[{"type":"string"},NOTICE_ITEM]}}`,
		},
		{
			name:     "list without items accepts the notice as is",
			upstream: `{"type":"array"}`,
			want:     `{"type":"array"}`,
		},
		{
			name:     "untyped schema with items allows the notice both ways",
			upstream: `{"items":{"type":"integer"}}`,
			want:     `{"properties":{"__mini":MINI},"items":{"anyOf":[{"type":"integer"},NOTICE_ITEM]}}`,
		},
		{
			name: "list branch of a root union allows a trailing notice",
			upstream: `{"oneOf":[{"type":"object","properties":{"id":{"type":"string"}}},` +
				`{"type":["null","array"],"items":{"type":"string"}}]}`,
			want: `{"properties":{"__mini":MINI},"anyOf":[{"type":"object","properties":{"id":{"type":"string"},"__mini":MINI}},` +
				`{"type":["null","array"],"items":{"anyOf":[{"type":"string"},NOTICE_ITEM]}}]}`,
		},
		{
			name:     "list items composed through allOf allow the notice",
			upstream: `{"type":"array","allOf":[{"items":{"type":"integer"}}]}`,
			want:     `{"type":"array","allOf":[{"properties":{"__mini":MINI},"items":{"anyOf":[{"type":"integer"},NOTICE_ITEM]}}]}`,
		},
		{
			name: "list branch built from typed anyOf alternatives allows the notice in each",
			upstream: `{"oneOf":[{"type":"null"},{"type":"array","anyOf":[` +
				`{"type":["null","array"],"items":{"type":"string"}},{"type":["null","array"],"items":{"type":"integer"}}]}]}`,
			want: `{"properties":{"__mini":MINI},"anyOf":[{"type":"null"},{"type":"array","anyOf":[` +
				`{"type":["null","array"],"items":{"anyOf":[{"type":"string"},NOTICE_ITEM]}},` +
				`{"type":["null","array"],"items":{"anyOf":[{"type":"integer"},NOTICE_ITEM]}}]}]}`,
		},
	})
}

func TestOutputSchema_FallsBackToRootTypeWhenTheNoticeCannotBePlaced(t *testing.T) {
	runOutputSchemaCases(t, []outputSchemaCase{
		{name: "string root", upstream: `{"type":"string","minLength":3}`, want: `{"type":"string"}`},
		{
			name:     "untyped $ref root may point at a list, so any value",
			upstream: `{"$ref":"#/$defs/Out","$defs":{"Out":{"type":"array"}}}`,
			want:     `{}`,
		},
		{
			name:     "nullable $ref root keeps null",
			upstream: `{"type":["object","null"],"$ref":"#/$defs/Out","$defs":{"Out":{"type":["object","null"]}}}`,
			want:     `{"type":["object","null"]}`,
		},
		{
			name:     "union branch through $ref",
			upstream: `{"type":"object","anyOf":[{"$ref":"#/$defs/Out"}],"$defs":{"Out":{"type":"object"}}}`,
			want:     `{"type":"object"}`,
		},
		{
			name:     "closed tuple",
			upstream: `{"type":"array","prefixItems":[{"type":"string"}],"items":false}`,
			want:     `{"type":"array"}`,
		},
		{
			name:     "positional items would reject a notice in their place",
			upstream: `{"type":"array","prefixItems":[{"type":"integer"},{"type":"integer"}]}`,
			want:     `{"type":"array"}`,
		},
		{
			name: "not reaching a loosened $defs through $ref",
			upstream: `{"type":"object","properties":{"value":{"not":{"$ref":"#/$defs/NonEmpty"}}},` +
				`"$defs":{"NonEmpty":{"type":"string","minLength":1}}}`,
			want: `{"type":"object"}`,
		},
		{
			name: "if reaching a loosened $defs through $ref",
			upstream: `{"type":"object","properties":{"value":{"if":{"$ref":"#/$defs/NonEmpty"},"then":{}}},` +
				`"$defs":{"NonEmpty":{"type":"string","minLength":1}}}`,
			want: `{"type":"object"}`,
		},
		{
			name:     "unknown dialect may assert keywords mini can't loosen",
			upstream: `{"$schema":"https://example.com/custom/schema","type":"object","x-min":1}`,
			want:     `{"type":"object"}`,
		},
	})
}

func TestOutputSchema_NonObjectSchemaIsLeftAlone(t *testing.T) {
	tool := map[string]any{"outputSchema": true}
	setProxyOutputSchema(tool)
	if tool["outputSchema"] != true {
		t.Errorf("boolean schema changed to %v", tool["outputSchema"])
	}
	tool = map[string]any{"outputSchema": nil}
	setProxyOutputSchema(tool)
	if _, present := tool["outputSchema"]; present {
		t.Error("null outputSchema is still advertised")
	}
}

func TestOutputSchema_NoticeSchemaMatchesTheNoticeMiniSends(t *testing.T) {
	meta := miniOutputMetaSchema()["properties"].(map[string]any)
	assertSchemaDescribes(t, meta, reflect.TypeFor[response.ProjectionMeta]())
	truncated := meta["truncated"].(map[string]any)["items"].(map[string]any)
	assertSchemaDescribes(t, truncated["properties"].(map[string]any), reflect.TypeFor[projection.Truncation]())
	for _, name := range truncated["required"].([]string) {
		if slices.Contains(omittedWhenEmpty(reflect.TypeFor[projection.Truncation]()), name) {
			t.Errorf("schema requires %q, which a truncation leaves out when empty", name)
		}
	}
}

func assertSchemaDescribes(t *testing.T, properties map[string]any, notice reflect.Type) {
	t.Helper()
	for i := range notice.NumField() {
		name, _, _ := strings.Cut(notice.Field(i).Tag.Get("json"), ",")
		if properties[name] == nil {
			t.Errorf("%s field %q has no schema", notice.Name(), name)
		}
	}
}

func omittedWhenEmpty(notice reflect.Type) []string {
	var names []string
	for i := range notice.NumField() {
		name, options, _ := strings.Cut(notice.Field(i).Tag.Get("json"), ",")
		if strings.Contains(options, "omitempty") {
			names = append(names, name)
		}
	}
	return names
}

func runOutputSchemaCases(t *testing.T, cases []outputSchemaCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var schema any
			if err := json.Unmarshal([]byte(tc.upstream), &schema); err != nil {
				t.Fatalf("upstream: %v", err)
			}
			tool := map[string]any{"outputSchema": schema}
			setProxyOutputSchema(tool)
			assertSameJSON(t, tool["outputSchema"], expandNoticeSchemas(tc.want))
		})
	}
}

func expandNoticeSchemas(want string) string {
	want = strings.ReplaceAll(want, "NOTICE_ITEM", mustJSON(miniNoticeItemSchema()))
	return strings.ReplaceAll(want, "MINI", mustJSON(miniOutputMetaSchema()))
}

func assertSameJSON(t *testing.T, got any, want string) {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want: %v", err)
	}
	if gotJSON, wantJSON := mustJSON(got), mustJSON(wantValue); gotJSON != wantJSON {
		t.Errorf("advertised outputSchema\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

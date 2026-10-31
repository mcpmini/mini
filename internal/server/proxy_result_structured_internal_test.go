//go:build test

package server

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuildProxyResult_projectsStructuredContentOnceAndMirrorsIt(t *testing.T) {
	object := map[string]any{"id": 1, "body": longBody}
	trimmed := withMini(
		map[string]any{"id": 1, "body": "abcde"},
		miniNotice(truncatedChars(".structuredContent | .body", 5)),
	)
	bodyPath := ".structuredContent | .body"
	trimmedList := []any{1, 2, map[string]any{"__mini": miniNotice(truncatedItems(".structuredContent | .", 2))}}
	cases := []proxyResultCase{
		{
			name:       "identical JSON text and structuredContent carry the same trimmed object",
			upstream:   map[string]any{"structuredContent": object, "content": []any{textBlock(jsonText(t, object))}},
			projection: bodyLimit,
			want:       map[string]any{"structuredContent": trimmed, "content": []any{textBlock(jsonText(t, trimmed))}},
			recovered:  map[string]any{bodyPath: longBody},
		},
		{
			name:       "pretty-printed mirror text is still recognized as a mirror",
			upstream:   map[string]any{"structuredContent": object, "content": []any{textBlock(prettyText(t, object))}},
			projection: bodyLimit,
			want:       map[string]any{"structuredContent": trimmed, "content": []any{textBlock(jsonText(t, trimmed))}},
			recovered:  map[string]any{bodyPath: longBody},
		},
		{
			name: "unchanged mirror keeps its upstream text byte for byte",
			upstream: map[string]any{
				"structuredContent": map[string]any{"id": 1},
				"content":           []any{textBlock(`{ "id": 1 }`)},
			},
			projection: bodyLimit,
			want: map[string]any{
				"structuredContent": map[string]any{"id": 1},
				"content":           []any{textBlock(`{ "id": 1 }`)},
			},
		},
		{
			name:       "different prose is kept and the notice trails",
			upstream:   map[string]any{"structuredContent": object, "content": []any{textBlock("summary")}},
			projection: bodyLimit,
			want: map[string]any{
				"structuredContent": trimmed,
				"content": []any{
					textBlock("summary"),
					trailingNotice(t, miniNotice(truncatedChars(bodyPath, 5))),
				},
			},
			recovered: map[string]any{bodyPath: longBody},
		},
		{
			name:       "array structuredContent carries the notice as its last element",
			upstream:   map[string]any{"structuredContent": []any{1, 2, 3, 4}, "content": []any{textBlock("list")}},
			projection: arrayLimit,
			want: map[string]any{
				"structuredContent": []any{
					1,
					2,
					map[string]any{"__mini": miniNotice(truncatedItems(".structuredContent | .", 2))},
				},
				"content": []any{
					textBlock("list"),
					trailingNotice(t, miniNotice(truncatedItems(".structuredContent | .", 2))),
				},
			},
			recovered: map[string]any{".structuredContent | .": []any{1, 2, 3, 4}},
		},
		{
			name: "mirrored array structuredContent carries the notice in both copies",
			upstream: map[string]any{
				"structuredContent": []any{1, 2, 3, 4},
				"content":           []any{textBlock(jsonText(t, []any{1, 2, 3, 4}))},
			},
			projection: arrayLimit,
			want: map[string]any{
				"structuredContent": trimmedList,
				"content":           []any{textBlock(jsonText(t, trimmedList))},
			},
			recovered: map[string]any{".structuredContent | .": []any{1, 2, 3, 4}},
		},
		{
			name: "structuredContent that already has __mini is not the carrier",
			upstream: map[string]any{
				"structuredContent": map[string]any{"__mini": "x", "body": longBody},
				"content":           []any{textBlock("summary")},
			},
			projection: bodyLimit,
			want: map[string]any{
				"structuredContent": map[string]any{"__mini": "x", "body": "abcde"},
				"content": []any{
					textBlock("summary"),
					trailingNotice(t, miniNotice(truncatedChars(bodyPath, 5))),
				},
			},
			recovered: map[string]any{bodyPath: longBody},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runProxyResultCase(t, tc) })
	}
}

func TestBuildProxyResult_largeIntegersSurviveARewrittenBlock(t *testing.T) {
	object := map[string]any{"id": json.Number("9007199254740993"), "body": longBody}
	raw := json.RawMessage(jsonText(t, map[string]any{"content": []any{textBlock(jsonText(t, object))}}))
	out, err := newResultServer(t).buildProxyResult(proxyResultParams{Raw: raw, ProjCfg: bodyLimit})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Raw, []byte(`\"id\":9007199254740993`)) {
		t.Errorf("large integer changed in the rewritten block: %s", out.Raw)
	}
}

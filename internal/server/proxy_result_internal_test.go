//go:build test

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

type proxyResultCase struct {
	name       string
	upstream   map[string]any
	projection *config.ProjectionConfig
	want       map[string]any
	recovered  map[string]any
}

const longBody = "abcdefghij"

var (
	bodyLimit   = &config.ProjectionConfig{StringLimits: map[string]int{"body": 5}}
	arrayLimit  = &config.ProjectionConfig{ArrayLimits: map[string]int{"default": 2}}
	filePattern = regexp.MustCompile(`file\\*":\\*"([0-9]+)`)
)

func textBlock(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func jsonText(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func prettyText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func miniNotice(truncated ...projection.Truncation) response.ProjectionMeta {
	return response.ProjectionMeta{Msg: response.ProxyMsg, File: "FILE", Truncated: truncated}
}

func truncatedChars(path string, n int) projection.Truncation {
	return projection.Truncation{JQPath: path, Chars: n}
}

func truncatedItems(path string, n int) projection.Truncation {
	return projection.Truncation{JQPath: path, Items: n}
}

func trailingNotice(t *testing.T, notice response.ProjectionMeta) map[string]any {
	return textBlock("\n" + jsonText(t, map[string]any{"__mini": notice}))
}

func withMini(obj map[string]any, notice response.ProjectionMeta) map[string]any {
	out := map[string]any{"__mini": notice}
	for k, v := range obj {
		out[k] = v
	}
	return out
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(jsonText(t, v)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func maskRecoveryFile(t *testing.T, result map[string]any) (masked map[string]any, file string) {
	t.Helper()
	raw := jsonText(t, result)
	if m := filePattern.FindStringSubmatch(raw); m != nil {
		file = m[1]
		raw = strings.ReplaceAll(raw, file, "FILE")
	}
	if err := json.Unmarshal([]byte(raw), &masked); err != nil {
		t.Fatal(err)
	}
	return masked, file
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func newResultServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: discardLogger()})
	t.Cleanup(srv.Close)
	return srv
}

func runProxyResultCase(t *testing.T, tc proxyResultCase) {
	t.Helper()
	srv := newResultServer(t)
	out, err := srv.buildProxyResult(
		proxyResultParams{Raw: json.RawMessage(jsonText(t, tc.upstream)), ProjCfg: tc.projection},
	)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	masked, file := maskRecoveryFile(t, result)
	if want := roundTrip(t, tc.want); !reflect.DeepEqual(masked, want) {
		t.Errorf("result:\n got %s\nwant %s", jsonText(t, masked), jsonText(t, want))
	}
	if len(tc.recovered) == 0 {
		if file != "" || countFiles(t, srv.store.Dir()) != 0 {
			t.Errorf("no recovery file expected, file=%q files=%d", file, countFiles(t, srv.store.Dir()))
		}
		return
	}
	if file == "" {
		t.Fatalf("expected a recovery file key in the notice: %v", result)
	}
	assertRecovered(t, srv, file, tc.recovered)
}

func assertRecovered(t *testing.T, srv *Server, file string, recovered map[string]any) {
	t.Helper()
	for path, want := range recovered {
		got, err := srv.handleRead(
			context.Background(),
			json.RawMessage(jsonText(t, map[string]string{"file": file, "filter": path})),
		)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		var value any
		if err := json.Unmarshal([]byte(got.(string)), &value); err != nil {
			t.Fatalf("read %q returned non-JSON %v: %v", path, got, err)
		}
		if expected := roundTrip(t, want); !reflect.DeepEqual(value, expected) {
			t.Errorf("read %q = %v, want %s", path, got, jsonText(t, want))
		}
	}
}

func TestBuildProxyResult_trimsContentBlocksInPlace(t *testing.T) {
	object := map[string]any{"id": 1, "body": longBody}
	trimmed := map[string]any{"id": 1, "body": "abcde"}
	objectPath := ".content[0].text | fromjson | .body"
	cases := []proxyResultCase{
		{
			name:     "untrimmed JSON object is returned as sent",
			upstream: map[string]any{"content": []any{textBlock(`{"id":1,"body":"abc"}`)}},
			want:     map[string]any{"content": []any{textBlock(`{"id":1,"body":"abc"}`)}},
		},
		{
			name:       "markup stripped within the limit is rewritten without a notice",
			upstream:   map[string]any{"content": []any{textBlock(`{"html":"<p>hello <b>world</b></p>"}`)}},
			projection: &config.ProjectionConfig{StripMarkup: true},
			want:       map[string]any{"content": []any{textBlock(`{"html":"hello world"}`)}},
		},
		{
			name:       "truncated object field puts the notice inside the object",
			upstream:   map[string]any{"content": []any{textBlock(jsonText(t, object))}},
			projection: bodyLimit,
			want: map[string]any{"content": []any{
				textBlock(jsonText(t, withMini(trimmed, miniNotice(truncatedChars(objectPath, 5))))),
			}},
			recovered: map[string]any{objectPath: longBody},
		},
		{
			name:       "limited array stays in place and the notice is a trailing block",
			upstream:   map[string]any{"content": []any{textBlock(`[1,2,3,4]`)}},
			projection: arrayLimit,
			want: map[string]any{"content": []any{
				textBlock(`[1,2]`),
				trailingNotice(t, miniNotice(truncatedItems(".content[0].text | fromjson | .", 2))),
			}},
			recovered: map[string]any{".content[0].text | fromjson | .": []any{1, 2, 3, 4}},
		},
		{
			name:     "prose over the limit is truncated with a trailing notice",
			upstream: map[string]any{"content": []any{textBlock(strings.Repeat("x", 2500))}},
			want: map[string]any{"content": []any{
				textBlock(strings.Repeat("x", 2000)),
				trailingNotice(t, miniNotice(truncatedChars(".content[0].text | .", 500))),
			}},
			recovered: map[string]any{".content[0].text | .": strings.Repeat("x", 2500)},
		},
		{
			name:       "notice goes in the one JSON object among text blocks",
			upstream:   map[string]any{"content": []any{textBlock("summary"), textBlock(jsonText(t, object))}},
			projection: bodyLimit,
			want: map[string]any{"content": []any{
				textBlock("summary"),
				textBlock(
					jsonText(
						t,
						withMini(trimmed, miniNotice(truncatedChars(".content[1].text | fromjson | .body", 5))),
					),
				),
			}},
			recovered: map[string]any{".content[1].text | fromjson | .body": longBody},
		},
		{
			name:       "upstream object that already has a __mini key is not used as the carrier",
			upstream:   map[string]any{"content": []any{textBlock(`{"__mini":"x","body":"abcdefghij"}`)}},
			projection: bodyLimit,
			want: map[string]any{"content": []any{
				textBlock(`{"__mini":"x","body":"abcde"}`),
				trailingNotice(t, miniNotice(truncatedChars(objectPath, 5))),
			}},
			recovered: map[string]any{objectPath: longBody},
		},
		{
			name: "embedded text resource over the limit is truncated",
			upstream: map[string]any{"content": []any{
				map[string]any{
					"type": "resource",
					"resource": map[string]any{
						"uri":      "file:///a.txt",
						"mimeType": "text/plain",
						"text":     strings.Repeat("y", 2500),
					},
				},
			}},
			want: map[string]any{"content": []any{
				map[string]any{
					"type": "resource",
					"resource": map[string]any{
						"uri":      "file:///a.txt",
						"mimeType": "text/plain",
						"text":     strings.Repeat("y", 2000),
					},
				},
				trailingNotice(t, miniNotice(truncatedChars(".content[0].resource.text | .", 500))),
			}},
			recovered: map[string]any{".content[0].resource.text | .": strings.Repeat("y", 2500)},
		},
		{
			name: "members mini doesn't model survive a rewrite (adversarial)",
			upstream: map[string]any{
				"futureField": 1,
				"content": []any{
					map[string]any{
						"type": "text", "text": strings.Repeat("x", 2500),
						"annotations": map[string]any{"audience": []any{"user"}}, "_meta": map[string]any{"k": "v"},
					},
					map[string]any{
						"type":  "resource",
						"_meta": map[string]any{"b": 1},
						"resource": map[string]any{
							"uri":   "file:///a.txt",
							"text":  strings.Repeat("y", 2500),
							"_meta": map[string]any{"r": 2},
						},
					},
					map[string]any{
						"type":        "image",
						"data":        "AAAA",
						"mimeType":    "image/png",
						"annotations": map[string]any{"priority": 1},
					},
				},
			},
			want: map[string]any{
				"futureField": 1,
				"content": []any{
					map[string]any{
						"type": "text", "text": strings.Repeat("x", 2000),
						"annotations": map[string]any{"audience": []any{"user"}}, "_meta": map[string]any{"k": "v"},
					},
					map[string]any{
						"type":  "resource",
						"_meta": map[string]any{"b": 1},
						"resource": map[string]any{
							"uri":   "file:///a.txt",
							"text":  strings.Repeat("y", 2000),
							"_meta": map[string]any{"r": 2},
						},
					},
					map[string]any{
						"type":        "image",
						"data":        "AAAA",
						"mimeType":    "image/png",
						"annotations": map[string]any{"priority": 1},
					},
					trailingNotice(t, miniNotice(
						truncatedChars(".content[0].text | .", 500),
						truncatedChars(".content[1].resource.text | .", 500),
					)),
				},
			},
			recovered: map[string]any{".content[1].resource.text | .": strings.Repeat("y", 2500)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runProxyResultCase(t, tc) })
	}
}

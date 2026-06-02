package server

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

func TestSanitizeLine(t *testing.T) {
	t.Run("passthrough", func(t *testing.T) {
		if got := sanitizeLine("hello"); got != "hello" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("newline replaced with space", func(t *testing.T) {
		got := sanitizeLine("line1\nline2")
		if strings.Contains(got, "\n") {
			t.Errorf("newline not replaced: %q", got)
		}
		if !strings.Contains(got, " ") {
			t.Errorf("expected space replacement: %q", got)
		}
	})
	t.Run("carriage return stripped", func(t *testing.T) {
		got := sanitizeLine("hello\r\nworld")
		if strings.Contains(got, "\r") {
			t.Errorf("carriage return not stripped: %q", got)
		}
	})
}

func TestSanitizeLine_truncation(t *testing.T) {
	t.Run("truncated at 80 with ellipsis", func(t *testing.T) {
		got := sanitizeLine(strings.Repeat("a", 100))
		if len(got) > 80 {
			t.Errorf("expected max 80 chars, got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("expected ... suffix, got %q", got)
		}
	})
	t.Run("exactly 80 chars not truncated", func(t *testing.T) {
		s := strings.Repeat("b", 80)
		if got := sanitizeLine(s); got != s {
			t.Error("80-char string should not be truncated")
		}
	})
}

var formatScalarCases = []struct {
	name string
	in   any
	want string
}{
	{"nil", nil, "-"},
	{"bool true", true, "true"},
	{"bool false", false, "-"},
	{"float64 zero", float64(0), "-"},
	{"float64 nonzero", float64(42), "42"},
	{"int nonzero", 7, "7"},
	{"int zero", 0, "-"},
	{"empty string", "", "-"},
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected %q to contain %q", got, want)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("expected %q not to contain %q", got, want)
	}
}

func assertUniformKeys(t *testing.T, items []any, want []string) {
	t.Helper()
	keys := uniformKeys(items)
	if len(keys) != len(want) {
		t.Fatalf("expected %v, got %v", want, keys)
	}
	for i, key := range want {
		if keys[i] != key {
			t.Fatalf("expected %v, got %v", want, keys)
		}
	}
}

func assertUniformKeysNil(t *testing.T, items []any) {
	t.Helper()
	if uniformKeys(items) != nil {
		t.Error("expected nil keys")
	}
}

func renderEnvelope(ok bool, data any) *response.Envelope {
	if !ok {
		return &response.Envelope{Error: "tool_error", Message: "something went wrong"}
	}
	return &response.Envelope{Data: data}
}

func TestFormatScalar(t *testing.T) {
	for _, tc := range formatScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatScalar(tc.in); got != tc.want {
				t.Errorf("formatScalar(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatScalar_strings(t *testing.T) {
	t.Run("newline replaced", func(t *testing.T) {
		if got := formatScalar("foo\nbar"); strings.Contains(got, "\n") {
			t.Errorf("newline not replaced: %q", got)
		}
	})
	t.Run("truncated at 80", func(t *testing.T) {
		got := formatScalar(strings.Repeat("x", 100))
		if len(got) > 80 {
			t.Errorf("not truncated at 80: len=%d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("expected ... suffix, got %q", got)
		}
	})
	t.Run("object marshalled as JSON", func(t *testing.T) {
		if got := formatScalar(map[string]any{"k": "v"}); !strings.HasPrefix(got, "{") {
			t.Errorf("expected JSON for object, got %q", got)
		}
	})
}

func TestUniformKeys_nilAndSingle(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		assertUniformKeysNil(t, nil)
	})
	t.Run("single item not worth a header", func(t *testing.T) {
		assertUniformKeysNil(t, []any{map[string]any{"a": 1}})
	})
}

func TestUniformKeys_uniform(t *testing.T) {
	t.Run("two items returns sorted keys", func(t *testing.T) {
		assertUniformKeys(t, []any{
			map[string]any{"z": 1, "a": 2},
			map[string]any{"z": 3, "a": 4},
		}, []string{"a", "z"})
	})
	t.Run("three items sorted", func(t *testing.T) {
		assertUniformKeys(t, []any{
			map[string]any{"z": 1, "a": 2, "m": 3},
			map[string]any{"z": 4, "a": 5, "m": 6},
			map[string]any{"z": 7, "a": 8, "m": 9},
		}, []string{"a", "m", "z"})
	})
}

func TestUniformKeys_nonUniform(t *testing.T) {
	t.Run("different keys returns nil", func(t *testing.T) {
		assertUniformKeysNil(t, []any{map[string]any{"a": 1}, map[string]any{"b": 2}})
	})
	t.Run("different key counts returns nil", func(t *testing.T) {
		assertUniformKeysNil(t, []any{map[string]any{"a": 1, "b": 2}, map[string]any{"a": 3}})
	})
	t.Run("first item not a map", func(t *testing.T) {
		assertUniformKeysNil(t, []any{"string", map[string]any{"a": 1}})
	})
	t.Run("later item not a map", func(t *testing.T) {
		assertUniformKeysNil(t, []any{map[string]any{"a": 1}, "string"})
	})
}

func TestRenderItemLine_numerics(t *testing.T) {
	t.Run("float zero skipped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"score": float64(0)}), "score")
	})
	t.Run("float nonzero included", func(t *testing.T) {
		assertContains(t, renderItemLine(map[string]any{"score": float64(9.5)}), "score:9.5")
	})
	t.Run("bool false skipped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"active": false}), "active")
	})
	t.Run("bool true shown as flag", func(t *testing.T) {
		assertContains(t, renderItemLine(map[string]any{"active": true}), "+active")
	})
}

func TestRenderItemLine_largeIntegerNoScientificNotation(t *testing.T) {
	// float64 values >= 1e9 must render as plain integers, not "1.195500437e+09".
	// Regression: classifyNumeric used %v which produces scientific notation.
	cases := []struct {
		name string
		val  float64
		want string
	}{
		{"repo id", 1195500437, "id:1195500437"},
		{"user id 8 digits", 10168637, "id:10168637"},
		{"large issue number", 79774, "id:79774"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderItemLine(map[string]any{"id": tc.val})
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
			if strings.Contains(got, "e+") || strings.Contains(got, "E+") {
				t.Errorf("scientific notation in output: %q", got)
			}
		})
	}
}

func TestWriteMapLines_largeIntegerNoScientificNotation(t *testing.T) {
	// writeMapLines also used %v for top-level scalars; same regression.
	env := renderEnvelope(true, map[string]any{
		"total_count": float64(1195500437),
		"items":       []any{},
	})
	got := RenderLines("svc", "tool", env)
	if strings.Contains(got, "e+") || strings.Contains(got, "E+") {
		t.Errorf("scientific notation in map scalar output: %q", got)
	}
	if !strings.Contains(got, "total_count:1195500437") {
		t.Errorf("expected integer rendering, got: %q", got)
	}
}

func TestRenderItemLine_strings(t *testing.T) {
	t.Run("empty string skipped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"name": ""}), "name")
	})
	t.Run("newline stripped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"title": "line1\nline2"}), "\n")
	})
	t.Run("truncated", func(t *testing.T) {
		got := renderItemLine(map[string]any{"body": strings.Repeat("y", 100)})
		parts := strings.SplitN(got, "body:", 2)
		if len(parts) < 2 || len(parts[1]) > 80 {
			t.Errorf("string value should be truncated to 80 chars, got %q", got)
		}
	})
}

func TestRenderItemLine_arrays(t *testing.T) {
	t.Run("string array joined", func(t *testing.T) {
		assertContains(t, renderItemLine(map[string]any{"tags": []any{"go", "test"}}), "tags:[go,test]")
	})
	t.Run("non-string elements skipped", func(t *testing.T) {
		got := renderItemLine(map[string]any{"mixed": []any{"str", 42, true}})
		if strings.Contains(got, "42") || strings.Contains(got, "true") {
			t.Errorf("non-string array elements should be skipped, got %q", got)
		}
	})
	t.Run("all-numeric array skipped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"nums": []any{1, 2, 3}}), "nums")
	})
	t.Run("nested object skipped", func(t *testing.T) {
		assertNotContains(t, renderItemLine(map[string]any{"meta": map[string]any{"x": 1}}), "meta")
	})
}

func TestFindPrimaryArray(t *testing.T) {
	t.Run("no arrays", func(t *testing.T) {
		k, arr := findPrimaryArray(map[string]any{"a": 1, "b": "str"})
		if k != "" || arr != nil {
			t.Errorf("expected empty key and nil array, got %q %v", k, arr)
		}
	})
	t.Run("single array", func(t *testing.T) {
		assertPrimaryArray(t, map[string]any{"items": []any{1, 2, 3}, "total": 3}, "items", 3)
	})
	t.Run("largest array wins", func(t *testing.T) {
		assertPrimaryArray(t, map[string]any{
			"small": []any{1},
			"big":   []any{1, 2, 3, 4, 5},
		}, "big", 5)
	})
}

func assertPrimaryArray(t *testing.T, data map[string]any, wantKey string, wantLen int) {
	t.Helper()
	k, arr := findPrimaryArray(data)
	if k != wantKey || len(arr) != wantLen {
		t.Errorf("expected %s array len=%d, got key=%q len=%d", wantKey, wantLen, k, len(arr))
	}
}

func TestIsScalarValue(t *testing.T) {
	scalars := []any{nil, true, false, 42, float64(3.14), "hello"}
	for _, v := range scalars {
		if !isScalarValue(v) {
			t.Errorf("expected %v to be scalar", v)
		}
	}
	if isScalarValue(map[string]any{}) {
		t.Error("map should not be scalar")
	}
	if isScalarValue([]any{}) {
		t.Error("slice should not be scalar")
	}
}

func makeEnvelope(ok bool, data any) *response.Envelope {
	return renderEnvelope(ok, data)
}

func TestRenderLines_header(t *testing.T) {
	t.Run("server.tool format", func(t *testing.T) {
		out := RenderLines("myserver", "mytool", makeEnvelope(true, "hello"))
		if !strings.HasPrefix(out, "[myserver.mytool]\n") {
			t.Errorf("expected header [myserver.mytool], got: %q", out)
		}
	})
	t.Run("file path included", func(t *testing.T) {
		e := makeEnvelope(true, "hello")
		path := "/tmp/resp.json"
		e.File = &path
		out := RenderLines("srv", "tool", e)
		if !strings.Contains(out, "file:/tmp/resp.json") {
			t.Errorf("expected file path in header, got: %q", out)
		}
	})
	t.Run("error shows code and message", func(t *testing.T) {
		out := RenderLines("srv", "tool", makeEnvelope(false, nil))
		if !strings.Contains(out, "ERROR tool_error") || !strings.Contains(out, "something went wrong") {
			t.Errorf("expected ERROR line with message, got: %q", out)
		}
	})
}

func TestRenderLines_data(t *testing.T) {
	t.Run("string data rendered inline", func(t *testing.T) {
		if out := RenderLines("srv", "tool", makeEnvelope(true, "hello world")); !strings.Contains(out, "hello world") {
			t.Errorf("expected string data, got: %q", out)
		}
	})
	t.Run("unknown scalar rendered as string", func(t *testing.T) {
		if out := RenderLines("srv", "tool", makeEnvelope(true, float64(42))); !strings.Contains(out, "42") {
			t.Errorf("expected scalar value in output, got: %q", out)
		}
	})
}

func TestRenderLines_collections(t *testing.T) {
	t.Run("top-level array produces one line per item", func(t *testing.T) {
		items := []any{map[string]any{"name": "alice"}, map[string]any{"name": "bob"}}
		out := RenderLines("srv", "tool", makeEnvelope(true, items))
		if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) < 4 {
			t.Errorf("expected at least 4 lines for 2-item array, got %d: %q", len(lines), out)
		}
	})
	t.Run("map with primary array renders scalars and items", func(t *testing.T) {
		data := map[string]any{"total": float64(2), "items": []any{"a", "b"}}
		out := RenderLines("srv", "tool", makeEnvelope(true, data))
		if !strings.Contains(out, "total:") || !strings.Contains(out, "a") {
			t.Errorf("expected scalar and array items in output, got: %q", out)
		}
	})
}

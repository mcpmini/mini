package toon

import (
	"strings"
	"testing"
)

func FuzzEncodeFromJSON(f *testing.F) {
	seeds := []string{
		`{"a":{"b":{"c":{"d":1}}}}`,
		`{"items":[{"id":1,"name":"Ada"},{"id":2,"name":"Bob"}]}`,
		`{"tags":["a","b,c","","true","-x"]}`,
		`{"pairs":[[1,2],[],["a"]]}`,
		`{"mixed":[1,{"a":1},"text",{},[{"id":1}]]}`,
		`{"data":{"meta":{"items":["x","y"]}},"data.meta.items":"literal"}`,
		`{"u":"héllo 世界 👋","k":" pad "}`,
		`{"big":123456789012345678901234567890,"tiny":1e-7,"neg":-0.5}`,
		`[{"id":1},{"id":2}]`,
		`[]`,
		`{}`,
		`"42"`,
		`null`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := FromJSON(data)
		if err != nil {
			t.Skip()
		}
		out, err := Encode(v)
		if err != nil {
			t.Fatalf("Encode failed on FromJSON-accepted input %q: %v", data, err)
		}
		emptyRootObject := v.Kind == KindObject && len(v.Fields) == 0
		if out == "" && !emptyRootObject {
			t.Fatalf("empty output for non-empty input %q", data)
		}
		assertLineInvariants(t, out)
	})
}

// assertLineInvariants enforces spec §12: no trailing newline, no trailing
// whitespace on any line, no blank lines.
func assertLineInvariants(t *testing.T, out string) {
	t.Helper()
	if out == "" {
		return
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatalf("output has trailing newline: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			t.Fatalf("output has blank line: %q", out)
		}
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Fatalf("line has trailing whitespace: %q", line)
		}
	}
}

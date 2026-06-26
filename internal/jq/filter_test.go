//go:build test

package jq_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/jq"
)

func TestEval_simpleField(t *testing.T) {
	data := []byte(`{"title":"hello","body":"world"}`)
	got, err := jq.Eval(context.Background(), data, ".title")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"hello"` {
		t.Errorf("got %q, want %q", got, `"hello"`)
	}
}

func TestEval_nestedArrayIndex(t *testing.T) {
	data := []byte(`{"items":[{"body":"first"},{"body":"second"}]}`)
	got, err := jq.Eval(context.Background(), data, ".items[0].body")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"first"` {
		t.Errorf("got %q, want %q", got, `"first"`)
	}
}

func TestEval_rootArrayIndex(t *testing.T) {
	data := []byte(`[{"body":"a"},{"body":"b"}]`)
	got, err := jq.Eval(context.Background(), data, ".[0].body")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"a"` {
		t.Errorf("got %q, want %q", got, `"a"`)
	}
}

func TestEval_multipleOutputs(t *testing.T) {
	data := []byte(`{"items":[{"title":"a"},{"title":"b"}]}`)
	got, err := jq.Eval(context.Background(), data, `.items[] | .title`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || lines[0] != `"a"` || lines[1] != `"b"` {
		t.Errorf("expected two newline-separated outputs, got %q", got)
	}
}

func TestEval_emptyResult(t *testing.T) {
	data := []byte(`[]`)
	got, err := jq.Eval(context.Background(), data, ".[]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty iterator should return empty string, got %q", got)
	}
}

func TestEval_invalidFilter(t *testing.T) {
	_, err := jq.Eval(context.Background(), []byte(`{}`), "not valid jq !!!")
	if err == nil {
		t.Error("expected error for invalid jq filter")
	}
}

func TestEval_nonJSONInput(t *testing.T) {
	_, err := jq.Eval(context.Background(), []byte("not json"), ".field")
	if err == nil {
		t.Error("expected error for non-JSON input")
	}
}

func TestEval_resultCountCapReturnsError(t *testing.T) {
	items := make([]any, 10001)
	for i := range items {
		items[i] = i
	}
	data, _ := json.Marshal(items)
	_, err := jq.Eval(context.Background(), data, ".[]")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("expected cap error, got %v", err)
	}
}

func TestEval_byteCapAtBoundary(t *testing.T) {
	// maxOutputBytes = 4MB; a JSON string of 4MB-2 bytes marshals to exactly 4MB (with quotes).
	atCap := strings.Repeat("x", 4*1024*1024-2)
	data, _ := json.Marshal(atCap)
	_, err := jq.Eval(context.Background(), data, ".")
	if err != nil {
		t.Errorf("result at exactly the cap should not error: %v", err)
	}

	overCap := strings.Repeat("x", 4*1024*1024-1)
	data, _ = json.Marshal(overCap)
	_, err = jq.Eval(context.Background(), data, ".")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("result one byte over cap should error, got: %v", err)
	}
}

func TestEval_missingFieldReturnsNull(t *testing.T) {
	data := []byte(`{"title":"hello"}`)
	got, err := jq.Eval(context.Background(), data, ".nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != "null" {
		t.Errorf("missing field should return %q, got %q", "null", got)
	}
}

func TestEval_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := jq.Eval(ctx, []byte(`{"a":1}`), ".a")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestEval_htmlCharactersNotEscaped(t *testing.T) {
	data := []byte(`{"body":"<html>a & b</html>"}`)
	got, err := jq.Eval(context.Background(), data, ".body")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"<html>a & b</html>"` {
		t.Errorf("HTML chars must not be escaped, got %q", got)
	}
}

func TestEval_largeIntegerPreserved(t *testing.T) {
	data := []byte(`{"id":1234567890123456789}`)
	got, err := jq.Eval(context.Background(), data, ".id")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1234567890123456789" {
		t.Errorf("large integer must not lose precision, got %q", got)
	}
}

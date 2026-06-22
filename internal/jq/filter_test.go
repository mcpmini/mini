//go:build test

package jq_test

import (
	"context"
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

func TestEval_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := jq.Eval(ctx, []byte(`{"a":1}`), ".a")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

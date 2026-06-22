//go:build test

package server

import (
	"context"
	"strings"
	"testing"
)

func TestApplyReadFilter_basicFilters(t *testing.T) {
	data := []byte(`{"name":"alice","scores":[10,20,30],"meta":{"active":true},"items":[{"title":"first"},{"title":"second"}]}`)

	cases := []struct {
		filter string
		want   string
	}{
		{filter: ".name", want: `"alice"`},
		{filter: ".scores", want: `[10,20,30]`},
		{filter: ".scores[1]", want: `20`},
		{filter: ".meta.active", want: `true`},
		{filter: ".items[0].title", want: `"first"`},
		{filter: ".items[1].title", want: `"second"`},
	}

	for _, c := range cases {
		t.Run(c.filter, func(t *testing.T) {
			out, err := applyReadFilter(context.Background(), data, c.filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != c.want {
				t.Errorf("filter %q: got %s, want %s", c.filter, out, c.want)
			}
		})
	}
}

func TestApplyReadFilter_multipleOutputs(t *testing.T) {
	data := []byte(`{"items":[{"title":"a"},{"title":"b"}]}`)
	out, err := applyReadFilter(context.Background(), data, `.items[] | .title`)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || lines[0] != `"a"` || lines[1] != `"b"` {
		t.Errorf("expected two newline-separated outputs, got %q", out)
	}
}

func TestApplyReadFilter_invalidFilter(t *testing.T) {
	_, err := applyReadFilter(context.Background(), []byte(`{}`), "!!!invalid")
	if err == nil {
		t.Error("expected error for invalid jq filter")
	}
}

func TestApplyReadFilter_NonJSON(t *testing.T) {
	_, err := applyReadFilter(context.Background(), []byte("not json"), ".field")
	if err == nil {
		t.Error("expected error for non-JSON input")
	}
}

//go:build test

package server

import (
	"testing"
)

func TestApplyReadFilter(t *testing.T) {
	data := []byte(`{"name":"alice","scores":[10,20,30],"meta":{"active":true},"items":[{"title":"first"},{"title":"second"}]}`)

	cases := []struct {
		filter  string
		want    string
		wantErr bool
	}{
		{filter: ".", want: string(data)},
		{filter: ".name", want: `"alice"`},
		{filter: ".scores", want: `[10,20,30]`},
		{filter: ".scores.[1]", want: `20`},
		{filter: ".meta.active", want: `true`},
		{filter: ".items.[0].title", want: `"first"`},
		{filter: ".items.[1].title", want: `"second"`},
		{filter: ".missing", wantErr: true},
		{filter: ".scores.[5]", wantErr: true},
		{filter: ".name.[0]", wantErr: true},
		{filter: "name", wantErr: true},
		{filter: ".[0]", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.filter, func(t *testing.T) {
			out, err := applyReadFilter(data, c.filter)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error for filter %q, got %s", c.filter, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(out) != c.want {
				t.Errorf("filter %q: got %s, want %s", c.filter, out, c.want)
			}
		})
	}
}

func TestApplyReadFilter_NonJSON(t *testing.T) {
	_, err := applyReadFilter([]byte("not json"), ".field")
	if err == nil {
		t.Error("expected error for non-JSON input")
	}
}

package main

import (
	"encoding/json"
	"testing"
)

func TestToolHints(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "absent"},
		{name: "null", raw: `null`},
		{name: "empty", raw: `{}`},
		{name: "false hints", raw: `{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":false}`},
		{name: "read only", raw: `{"readOnlyHint":true}`, want: "read-only"},
		{name: "writes", raw: `{"destructiveHint":true,"idempotentHint":true}`, want: "destructive, idempotent"},
		{name: "external", raw: `{"openWorldHint":true}`, want: "open-world"},
		{name: "read only ignores write hints", raw: `{"readOnlyHint":true,"destructiveHint":true,"idempotentHint":true,"openWorldHint":true}`, want: "read-only, open-world"},
		{name: "unknown hints", raw: `{"futureHint":true,"title":"A title"}`},
		{name: "malformed", raw: `{`},
		{name: "invalid type", raw: `{"readOnlyHint":true,"openWorldHint":"yes"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolHints(json.RawMessage(tc.raw)); got != tc.want {
				t.Errorf("hints = %q, want %q", got, tc.want)
			}
		})
	}
}

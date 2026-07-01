//go:build test

package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseProxyToolName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSrv   string
		wantTool  string
		wantError bool
	}{
		{name: "valid", input: "a__b", wantSrv: "a", wantTool: "b"},
		{name: "tool with double underscore", input: "a__b__c", wantSrv: "a", wantTool: "b__c"},
		{name: "no separator", input: "ab", wantError: true},
		{name: "empty server", input: "__b", wantError: true},
		{name: "empty tool", input: "a__", wantError: true},
		{name: "both empty", input: "__", wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, tool, err := parseProxyToolName(tc.input)
			if tc.wantError {
				if err == nil {
					t.Errorf("parseProxyToolName(%q): expected error, got srv=%q tool=%q", tc.input, srv, tool)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProxyToolName(%q): unexpected error: %v", tc.input, err)
			}
			if srv != tc.wantSrv || tool != tc.wantTool {
				t.Errorf("parseProxyToolName(%q) = (%q, %q), want (%q, %q)", tc.input, srv, tool, tc.wantSrv, tc.wantTool)
			}
		})
	}
}

func TestParseProxyRequest_MissingOrNullArguments(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null")} {
		req, err := parseProxyRequest(raw)
		if err != nil {
			t.Fatalf("parseProxyRequest(%q): unexpected error: %v", raw, err)
		}
		if len(req.Args) != 0 {
			t.Errorf("parseProxyRequest(%q): expected empty args, got %v", raw, req.Args)
		}
		if req.Controls.Projection != "" {
			t.Errorf("parseProxyRequest(%q): expected empty projection control, got %q", raw, req.Controls.Projection)
		}
	}
}

func TestParseProxyRequest_ArgsForwarded(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"args":{"state":"open","limit":5}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Args["state"] != "open" || req.Args["limit"] != float64(5) {
		t.Errorf("args not forwarded correctly: %v", req.Args)
	}
}

func TestParseProxyRequest_MissingArgsKeyDefaultsToEmpty(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"__mini":{"projection":"raw"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Args) != 0 {
		t.Errorf("expected empty args when \"args\" key absent, got %v", req.Args)
	}
	if req.Controls.Projection != "raw" {
		t.Errorf("expected projection=raw, got %q", req.Controls.Projection)
	}
}

func TestParseProxyRequest_NestedMiniKeyInArgsPreserved(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"args":{"__mini":"upstream-owned-field"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Args["__mini"] != "upstream-owned-field" {
		t.Errorf("expected nested args.__mini to be preserved as ordinary data, got %v", req.Args)
	}
}

func TestParseProxyRequest_ProjectionControlValues(t *testing.T) {
	tests := []struct {
		name      string
		mini      string
		want      string
		wantError bool
	}{
		{name: "default", mini: `{"projection":"default"}`, want: "default"},
		{name: "raw", mini: `{"projection":"raw"}`, want: "raw"},
		{name: "empty object", mini: `{}`, want: ""},
		{name: "invalid value", mini: `{"projection":"summary"}`, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := parseProxyRequest(json.RawMessage(`{"__mini":` + tc.mini + `}`))
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for __mini=%s", tc.mini)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.Controls.Projection != tc.want {
				t.Errorf("projection = %q, want %q", req.Controls.Projection, tc.want)
			}
		})
	}
}

func TestParseProxyRequest_RejectsLegacyFlatFields(t *testing.T) {
	_, err := parseProxyRequest(json.RawMessage(`{"state":"open","limit":5}`))
	if err == nil {
		t.Fatal("expected error for legacy flat call")
	}
	if !containsAll(err.Error(), "args", "state", "limit") {
		t.Errorf("legacy rejection error should be actionable, got: %v", err)
	}
}

func TestParseProxyRequest_ArgsMustBeObject(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"args":"not an object"}`),
		json.RawMessage(`{"args":[1,2,3]}`),
		json.RawMessage(`{"args":5}`),
	} {
		if _, err := parseProxyRequest(raw); err == nil {
			t.Errorf("parseProxyRequest(%s): expected error for non-object args", raw)
		}
	}
}

func TestParseProxyRequest_MiniMustBeObject(t *testing.T) {
	if _, err := parseProxyRequest(json.RawMessage(`{"__mini":"raw"}`)); err == nil {
		t.Error("expected error for non-object __mini")
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

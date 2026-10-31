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
				t.Errorf(
					"parseProxyToolName(%q) = (%q, %q), want (%q, %q)",
					tc.input,
					srv,
					tool,
					tc.wantSrv,
					tc.wantTool,
				)
			}
		})
	}
}

func TestParseProxyRequest_MissingOrNullArguments(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null"), json.RawMessage("{}")} {
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

func TestParseProxyRequest_FlatArgumentsForwardedWithoutControlKey(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"state":"open","limit":5,"__mini":{"projection":"raw"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"state": "open", "limit": json.Number("5")}
	if len(req.Args) != len(want) || req.Args["state"] != "open" || req.Args["limit"] != json.Number("5") {
		t.Errorf("args = %v, want %v (__mini must not be forwarded)", req.Args, want)
	}
	if req.Controls.Projection != projectionRaw {
		t.Errorf("projection = %q, want raw", req.Controls.Projection)
	}
}

func TestParseProxyRequest_ArgsKeyIsUpstreamArgument(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"args":{"state":"open"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested, ok := req.Args["args"].(map[string]any)
	if !ok || nested["state"] != "open" {
		t.Errorf("expected args key forwarded as upstream argument named args, got %v", req.Args)
	}
}

func TestParseProxyRequest_MissingMiniKeyLeavesProjectionDefault(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"id":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Args["id"] != "x" || req.Controls.Projection != "" {
		t.Errorf("got args=%v projection=%q, want id=x and default projection", req.Args, req.Controls.Projection)
	}
}

func TestParseProxyRequest_NestedMiniKeyInArgsPreserved(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"args":{"__mini":"upstream-owned-field"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested, ok := req.Args["args"].(map[string]any)
	if !ok || nested["__mini"] != "upstream-owned-field" {
		t.Errorf("expected nested __mini inside args to be preserved as ordinary data, got %v", req.Args)
	}
}

func TestParseProxyRequest_ProjectionControlValues(t *testing.T) {
	tests := []struct {
		name      string
		mini      string
		want      projectionMode
		wantError bool
	}{
		{name: "default", mini: `{"projection":"default"}`, want: projectionDefault},
		{name: "raw", mini: `{"projection":"raw"}`, want: projectionRaw},
		{name: "empty object", mini: `{}`, want: ""},
		{name: "null", mini: `null`, want: ""},
		{name: "invalid value", mini: `{"projection":"summary"}`, wantError: true},
		{name: "non-object", mini: `"raw"`, wantError: true},
		{name: "misspelled key (adversarial)", mini: `{"projectoin":"raw"}`, wantError: true},
		{name: "extra key", mini: `{"projection":"raw","extra":true}`, wantError: true},
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

func TestParseProxyRequest_ArgumentsMustBeObject(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`"not an object"`),
		json.RawMessage(`[1,2,3]`),
		json.RawMessage(`5`),
	} {
		if _, err := parseProxyRequest(raw); err == nil {
			t.Errorf("parseProxyRequest(%s): expected error for non-object arguments", raw)
		}
	}
}

func TestParseProxyRequest_MiniMustBeObject(t *testing.T) {
	if _, err := parseProxyRequest(json.RawMessage(`{"__mini":"raw"}`)); err == nil {
		t.Error("expected error for non-object __mini")
	}
}

func TestParseProxyRequest_PreservesLargeIntegers(t *testing.T) {
	req, err := parseProxyRequest(json.RawMessage(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(req.Args)
	if !strings.Contains(string(b), "9007199254740993") {
		t.Errorf("large integer corrupted: got %s", b)
	}
}

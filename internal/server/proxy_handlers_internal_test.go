//go:build test

package server

import "testing"

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

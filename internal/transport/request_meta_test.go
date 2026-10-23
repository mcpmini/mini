//go:build test

package transport

import (
	"encoding/json"
	"testing"
)

func TestValidateModernRequestMeta(t *testing.T) {
	cases := []struct {
		name    string
		params  string
		wantErr bool
	}{
		{name: "absent legacy metadata", params: `null`},
		{name: "unrelated legacy metadata", params: `{"_meta":{"trace":"abc"}}`},
		{
			name: "empty modern capabilities",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}`,
		},
		{
			name: "extra modern metadata",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"trace":"abc"}}`,
		},
		{name: "missing version", params: `{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}`, wantErr: true},
		{
			name:    "missing capabilities",
			params:  `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`,
			wantErr: true,
		},
		{
			name: "empty version",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"",` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}`,
			wantErr: true,
		},
		{
			name: "wrong version type",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":7,` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}`,
			wantErr: true,
		},
		{
			name:    "client info signals modern intent",
			params:  `{"_meta":{"io.modelcontextprotocol/clientInfo":{}}}`,
			wantErr: true,
		},
		{name: "null metadata", params: `{"_meta":null}`, wantErr: true},
		{name: "scalar metadata", params: `{"_meta":"modern"}`, wantErr: true},
		{name: "array metadata", params: `{"_meta":[]}`, wantErr: true},
		{
			name: "null capabilities",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":null}}`,
			wantErr: true,
		},
		{
			name: "scalar capabilities",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":true}}`,
			wantErr: true,
		},
		{
			name: "array capabilities",
			params: `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":[]}}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModernRequestMeta(json.RawMessage(tc.params))
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateModernRequestMeta(%s) error = %v, wantErr %t", tc.params, err, tc.wantErr)
			}
		})
	}
}

func TestModernRequestRejectsNonObjectParams(t *testing.T) {
	if _, err := prepareModernParams(json.RawMessage(`[]`), ModernVersions[0]); err == nil ||
		err.Error() != "modern request params must be a JSON object" {
		t.Fatalf("prepareModernParams error = %v", err)
	}
}

func TestValidateModernRequestMetaParamsShapes(t *testing.T) {
	if err := ValidateModernRequestMeta(json.RawMessage(`{"_meta":`)); err == nil {
		t.Fatal("invalid JSON params passed validation")
	}
	if err := ValidateModernRequestMeta(json.RawMessage(`[1]`)); err != nil {
		t.Fatalf("positional params rejected: %v", err)
	}
}

func TestHasModernRequestIntent(t *testing.T) {
	tests := []struct {
		params string
		want   bool
	}{
		{``, false},
		{`{"cursor":"x"}`, false},
		{`{"_meta":{"trace":"abc"}}`, false},
		{`[{"_meta":{"io.modelcontextprotocol/clientInfo":{}}}]`, false},
		{`{"_meta":`, false},
		{`{"_meta":{"io.modelcontextprotocol/clientInfo":{}}}`, true},
		{`{"_meta":{"io.modelcontextprotocol/protocolVersion":"x"}}`, true},
		{`{"_meta":"modern"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.params, func(t *testing.T) {
			if got := HasModernRequestIntent(json.RawMessage(tc.params)); got != tc.want {
				t.Fatalf("HasModernRequestIntent(%s) = %v, want %v", tc.params, got, tc.want)
			}
		})
	}
}

func TestPrepareModernParamsAddsMissingMetadata(t *testing.T) {
	prepare := func(params string) map[string]json.RawMessage {
		t.Helper()
		prepared, err := prepareModernParams(json.RawMessage(params), ModernVersions[0])
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		}
		if err := json.Unmarshal(prepared, &fields); err != nil {
			t.Fatal(err)
		}
		return fields.Meta
	}
	for _, params := range []string{``, `null`, `{}`} {
		meta := prepare(params)
		if string(meta[protocolVersionMeta]) != `"`+ModernVersions[0]+`"` || meta[clientInfoMeta] == nil ||
			string(meta[clientCapabilitiesMeta]) != `{}` {
			t.Errorf(
				"params %q prepared metadata = %v, want version, mini's client info and empty capabilities",
				params,
				meta,
			)
		}
	}
	meta := prepare(`{"_meta":{"io.modelcontextprotocol/clientInfo":{"name":"agent"},"trace":1}}`)
	if string(meta[clientInfoMeta]) != `{"name":"agent"}` || string(meta["trace"]) != `1` {
		t.Fatalf("prepared metadata = %v, want the caller's client info and trace kept", meta)
	}
	if _, err := prepareModernParams(json.RawMessage(`{"_meta":[]}`), ModernVersions[0]); err == nil {
		t.Fatal("non-object _meta was accepted")
	}
}

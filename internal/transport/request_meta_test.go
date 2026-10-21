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

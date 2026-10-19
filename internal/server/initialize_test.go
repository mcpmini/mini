//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	in := bytes.NewReader(rpc("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, &out) }()
	<-done

	resp := parseResponse(t, out.Bytes())
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("unexpected protocol version: %v", result["protocolVersion"])
	}
}

// TestInitialize_CapabilitiesListChanged verifies that the server declares
// tools.listChanged:true — required when the server emits tools/list_changed notifications.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L28
func TestInitialize_CapabilitiesListChanged(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	msgs := serveAll(t, srv)
	for _, m := range msgs {
		if m["id"] != float64(1) {
			continue
		}
		result, _ := m["result"].(map[string]any)
		caps, _ := result["capabilities"].(map[string]any)
		tools, _ := caps["tools"].(map[string]any)
		if lc, _ := tools["listChanged"].(bool); !lc {
			t.Errorf("capabilities.tools.listChanged must be true, got: %v", tools)
		}
		return
	}
	t.Fatal("no initialize response found")
}

func TestInitialize_versionNegotiation(t *testing.T) {
	type versionCase struct {
		name    string
		version string
		present bool
		want    string
	}
	tests := make([]versionCase, 0, len(transport.LegacyVersions)+3)
	for _, version := range transport.LegacyVersions {
		tests = append(tests, versionCase{name: version, version: version, present: true, want: version})
	}
	for _, version := range []string{"2026-07-28", "99.99.99"} {
		tests = append(
			tests,
			versionCase{name: version, version: version, present: true, want: transport.LegacyVersions[0]},
		)
	}
	tests = append(tests, versionCase{name: "absent", want: transport.LegacyVersions[0]})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			params := map[string]any{
				"capabilities": map[string]any{},
				"clientInfo":   map[string]any{"name": "test", "version": "0"},
			}
			if tc.present {
				params["protocolVersion"] = tc.version
			}
			resp := serve(t, srv, rpc("initialize", params))
			if resp["error"] != nil {
				t.Fatalf("initialize error for client version %q: %v", tc.version, resp["error"])
			}
			result, _ := resp["result"].(map[string]any)
			got, _ := result["protocolVersion"].(string)
			if got != tc.want {
				t.Errorf("server protocolVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInitialize_serverInfoPresent verifies the initialize response contains serverInfo
// with non-empty name and version fields.
// Spec: "The server MUST respond with its own capabilities and information."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L79
func TestInitialize_serverInfoPresent(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, rpc("initialize", map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	result, _ := resp["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] == "" || info["name"] == nil {
		t.Errorf("serverInfo.name must be non-empty, got: %v", info)
	}
	if info["version"] == "" || info["version"] == nil {
		t.Errorf("serverInfo.version must be non-empty, got: %v", info)
	}
}

// TestInitialize_doubleInitialize verifies that sending initialize twice is handled
// gracefully (idempotent). The spec does not prohibit re-initialization.
func TestInitialize_doubleInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	secondInit := rpc("initialize", map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	var second map[string]any
	json.Unmarshal(bytes.TrimSpace(secondInit), &second) //nolint:errcheck
	second["id"] = 99
	secondInit, _ = json.Marshal(second)
	secondInit = append(secondInit, '\n')

	msgs := serveAll(t, srv, secondInit)
	for _, m := range msgs {
		if m["id"] == float64(99) {
			if m["error"] != nil {
				t.Errorf("second initialize returned error: %v", m["error"])
			}
			result, _ := m["result"].(map[string]any)
			if result["protocolVersion"] == nil {
				t.Errorf("second initialize response missing protocolVersion: %v", m)
			}
			return
		}
	}
	t.Error("no response for second initialize found")
}

//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestInitialize(t *testing.T) {
	srv := newTestServer(t)
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
	srv := newTestServer(t)
	msgs := serveAllProxy(t, srv)
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

// TestInitialize_versionNegotiation verifies that the server always responds with its own
// supported version regardless of what the client requests. Clients that cannot handle
// the server's version SHOULD disconnect (the client's responsibility, not the server's).
// Spec: "If the server supports the requested protocol version, it MUST respond with the same
// version. Otherwise, the server MUST respond with another protocol version it supports."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L128
func TestInitialize_versionNegotiation(t *testing.T) {
	for _, clientVer := range []string{"2024-11-05", "2025-03-26", "99.99.99", ""} {
		t.Run("client="+clientVer, func(t *testing.T) {
			srv := newTestServer(t)
			resp := serve(t, srv, rpc("initialize", map[string]any{
				"protocolVersion": clientVer,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "0"},
			}))
			if resp["error"] != nil {
				t.Fatalf("initialize error for client version %q: %v", clientVer, resp["error"])
			}
			result, _ := resp["result"].(map[string]any)
			got, _ := result["protocolVersion"].(string)
			if got != transport.ProtocolVersion {
				t.Errorf("server protocolVersion = %q, want %q", got, transport.ProtocolVersion)
			}
		})
	}
}

// TestInitialize_serverInfoPresent verifies the initialize response contains serverInfo
// with non-empty name and version fields.
// Spec: "The server MUST respond with its own capabilities and information."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L79
func TestInitialize_serverInfoPresent(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
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
	srv := newTestServer(t)
	secondInit := rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
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

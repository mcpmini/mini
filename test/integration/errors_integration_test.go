//go:build integration

package integration_test

import (
	"fmt"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestIntegrationError_upstreamRPCError(t *testing.T) {
	client := faultServer(t,
		map[string]string{"get_item": `{"id":1}`},
		map[string]any{"tool": "get_item", "method": "tools/call", "type": "rpc_error", "message": "upstream exploded"},
		"")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error == "" {
		t.Error("rpc_error fault should produce ok=false in envelope")
	}
}

func TestIntegrationError_toolTimeout(t *testing.T) {
	client := faultServer(t,
		map[string]string{"get_item": `{"id":1}`},
		map[string]any{"tool": "get_item", "method": "tools/call", "type": "delay", "delay_ms": 2000},
		"300ms")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error == "" {
		t.Error("timed-out tool should produce ok=false in envelope")
	}
}

func TestIntegrationError_unknownServer(t *testing.T) {
	_, isErr := quickServer(t, map[string]string{"x": `{}`}).execToolAllowError("doesnotexist", "x", nil)
	if !isErr {
		t.Error("expected isError=true for unknown server")
	}
}

func TestIntegrationError_unknownTool(t *testing.T) {
	_, isErr := quickServer(t, map[string]string{"x": `{}`}).execToolAllowError("svc", "nonexistent", nil)
	if !isErr {
		t.Error("expected isError=true for unknown tool")
	}
}

// TestIntegrationError_upstreamNeverStarts verifies that mini starts successfully even
// when a configured upstream binary does not exist — the server degrades
// gracefully (logs a warning) rather than refusing to start entirely. This
// is important so that one broken server config doesn't block all other
// upstreams from working.
func TestIntegrationError_upstreamNeverStarts(t *testing.T) {
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "bad", Command: "/nonexistent_binary_xyz_does_not_exist"})

	stdin, scanner := startMiniCmd(t, cfg)
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	// Server must respond to initialize (not crash)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
}

func TestIntegrationError_malformedRequest(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1}`})
	fmt.Fprint(client.stdin, "NOT_VALID_JSON_GARBAGE\n")
	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Error("server should continue serving after a malformed request")
	}
}

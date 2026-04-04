//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestError_upstreamRPCError(t *testing.T) {
	client := faultServer(t,
		map[string]string{"get_item": `{"id":1}`},
		map[string]any{"tool": "get_item", "method": "tools/call", "type": "rpc_error", "message": "upstream exploded"},
		"")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.OK {
		t.Error("rpc_error fault should produce ok=false in envelope")
	}
}

func TestError_toolTimeout(t *testing.T) {
	client := faultServer(t,
		map[string]string{"get_item": `{"id":1}`},
		map[string]any{"tool": "get_item", "method": "tools/call", "type": "delay", "delay_ms": 2000},
		"300ms")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.OK {
		t.Error("timed-out tool should produce ok=false in envelope")
	}
}

func TestError_unknownServer(t *testing.T) {
	_, isErr := quickServer(t, map[string]string{"x": `{}`}).execToolAllowError("doesnotexist", "x", nil)
	if !isErr {
		t.Error("expected isError=true for unknown server")
	}
}

func TestError_unknownTool(t *testing.T) {
	_, isErr := quickServer(t, map[string]string{"x": `{}`}).execToolAllowError("svc", "nonexistent", nil)
	if !isErr {
		t.Error("expected isError=true for unknown tool")
	}
}

func TestError_upstreamNeverStarts(t *testing.T) {
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "bad", "name: bad\ncommand: /nonexistent_binary_xyz_does_not_exist\n")

	cmd := exec.Command(miniBin, "--config", cfg, "serve")
	cmd.Stdin, _ = os.Open(os.DevNull)
	if err := cmd.Run(); err == nil {
		t.Error("mini should exit non-zero when upstream binary does not exist")
	}
}

func TestError_malformedRequest(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1}`})
	fmt.Fprint(client.stdin, "NOT_VALID_JSON_GARBAGE\n")
	e := client.execEnvelope("svc", "get_item", nil)
	if !e.OK {
		t.Error("server should continue serving after a malformed request")
	}
}

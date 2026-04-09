//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHTTP_basicToolCall(t *testing.T) {
	_, client := httpServer(t, nil)
	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Fatalf("expected ok=true, got: %+v", e)
	}
}

func TestHTTP_429ExhaustsRetriesReturnsError(t *testing.T) {
	_, client := httpServer(t, func(int) (int, []byte) { return 429, []byte("0") })
	if envelopeOK(t, client, "svc", "get_item") {
		t.Error("expected error after exhausting retries on 429")
	}
}

func TestHTTP_500ReturnsError(t *testing.T) {
	_, client := httpServer(t, func(int) (int, []byte) { return 500, nil })
	if envelopeOK(t, client, "svc", "get_item") {
		t.Error("expected error on HTTP 500")
	}
}

func TestHTTP_401ReturnsError(t *testing.T) {
	_, client := httpServer(t, func(int) (int, []byte) { return 401, nil })
	if envelopeOK(t, client, "svc", "get_item") {
		t.Error("expected error on HTTP 401")
	}
}

func TestHTTP_503ReturnsError(t *testing.T) {
	_, client := httpServer(t, func(int) (int, []byte) { return 503, nil })
	if envelopeOK(t, client, "svc", "get_item") {
		t.Error("expected error on HTTP 503")
	}
}

func TestHTTP_403ReturnsError(t *testing.T) {
	_, client := httpServer(t, func(int) (int, []byte) { return 403, nil })
	if envelopeOK(t, client, "svc", "get_item") {
		t.Error("expected error on HTTP 403")
	}
}

func TestHTTP_addServerSSRFBlocked(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	client := startServer(t, cfg)
	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "x", "transport": "sse", "url": "http://127.0.0.1/mcp"},
		},
	})
	var result struct {
		IsError bool                                   `json:"isError"`
		Content []struct{ Text string `json:"text"` } `json:"content"`
	}
	json.Unmarshal(raw, &result) //nolint:errcheck
	if !result.IsError {
		t.Error("expected isError=true for SSRF-blocked private IP")
	}
	if len(result.Content) > 0 && !strings.Contains(result.Content[0].Text, "private") {
		t.Errorf("error should mention private address, got: %s", result.Content[0].Text)
	}
}

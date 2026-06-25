//go:build integration

package integration_test

import (
	"fmt"
	"testing"
)

func TestAuth_bearerTokenSentToUpstream(t *testing.T) {
	f, gotAuth := authCapturingMCP(t, "Authorization")
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", fmt.Sprintf(
		"name: svc\ntransport: sse\nurl: %s\nauth:\n  type: bearer\n  token: my-secret-token\n", f.srv.URL))

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotAuth.Load().(string); got != "Bearer my-secret-token" {
		t.Errorf("expected Bearer my-secret-token, got %q", got)
	}
}

func TestAuth_apiKeySentToUpstream(t *testing.T) {
	f, gotKey := authCapturingMCP(t, "X-Api-Key")
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", fmt.Sprintf(
		"name: svc\ntransport: sse\nurl: %s\nauth:\n  type: apikey\n  header: X-Api-Key\n  token: my-api-key\n", f.srv.URL))

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotKey.Load().(string); got != "my-api-key" {
		t.Errorf("expected my-api-key, got %q", got)
	}
}

func TestAuth_staticHeaderForwarded(t *testing.T) {
	f, gotHeader := authCapturingMCP(t, "X-Custom-Key")
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", fmt.Sprintf(
		"name: svc\ntransport: sse\nurl: %s\nheaders:\n  X-Custom-Key: custom-value\n", f.srv.URL))

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotHeader.Load().(string); got != "custom-value" {
		t.Errorf("expected custom-value, got %q", got)
	}
}

func TestAuth_noTokenNoAuthHeader(t *testing.T) {
	f, gotAuth := authCapturingMCP(t, "Authorization")
	cfg := t.TempDir()
	writeHTTPServerYAML(t, cfg, "svc", f.srv.URL)

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotAuth.Load().(string); got != "" {
		t.Errorf("expected no Authorization header without auth config, got %q", got)
	}
}

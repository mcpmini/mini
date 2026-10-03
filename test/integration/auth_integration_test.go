//go:build integration

package integration_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestIntegrationAuth_bearerTokenSentToUpstream(t *testing.T) {
	f, gotAuth := authCapturingMCP(t, "Authorization")
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Auth: &config.AuthConfig{
			Type:  "bearer",
			Token: "my-secret-token",
		},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotAuth.Load().(string); got != "Bearer my-secret-token" {
		t.Errorf("expected Bearer my-secret-token, got %q", got)
	}
}

func TestIntegrationAuth_apiKeySentToUpstream(t *testing.T) {
	f, gotKey := authCapturingMCP(t, "X-Api-Key")
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Auth: &config.AuthConfig{
			Type:   "apikey",
			Token:  "my-api-key",
			Header: "X-Api-Key",
		},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotKey.Load().(string); got != "my-api-key" {
		t.Errorf("expected my-api-key, got %q", got)
	}
}

func TestIntegrationAuth_staticHeaderForwarded(t *testing.T) {
	f, gotHeader := authCapturingMCP(t, "X-Custom-Key")
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Headers:   map[string]string{"X-Custom-Key": "custom-value"},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotHeader.Load().(string); got != "custom-value" {
		t.Errorf("expected custom-value, got %q", got)
	}
}

func TestIntegrationAuth_noTokenNoAuthHeader(t *testing.T) {
	f, gotAuth := authCapturingMCP(t, "Authorization")
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "svc", Transport: "sse", URL: f.srv.URL})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got, _ := gotAuth.Load().(string); got != "" {
		t.Errorf("expected no Authorization header without auth config, got %q", got)
	}
}

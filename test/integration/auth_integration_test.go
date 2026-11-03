//go:build integration

package integration_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestIntegrationAuth_bearerTokenSentToUpstream(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
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

	if got := f.lastHeader("Authorization"); got != "Bearer my-secret-token" {
		t.Errorf("expected Bearer my-secret-token, got %q", got)
	}
}

func TestIntegrationAuth_apiKeySentToUpstream(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
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

	if got := f.lastHeader("X-Api-Key"); got != "my-api-key" {
		t.Errorf("expected my-api-key, got %q", got)
	}
}

func TestIntegrationAuth_staticHeaderForwarded(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Headers:   map[string]string{"X-Custom-Key": "custom-value"},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got := f.lastHeader("X-Custom-Key"); got != "custom-value" {
		t.Errorf("expected custom-value, got %q", got)
	}
}

func TestIntegrationAuth_noTokenNoAuthHeader(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "svc", Transport: "sse", URL: f.srv.URL})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got := f.lastHeader("Authorization"); got != "" {
		t.Errorf("expected no Authorization header without auth config, got %q", got)
	}
}

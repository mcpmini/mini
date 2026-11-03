//go:build test

package server_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
)

func TestAuthHeader_bearer(t *testing.T) {
	httpSrv := mcptest.NewHTTP(t, mcptest.Upstream{})

	srv := newTestServer(t, server.Params{})
	sc := config.ServerConfig{
		Name:      "secured",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "bearer", Token: "tok-123"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream with bearer auth failed: %v", err)
	}
	if got := httpSrv.Last("tools/list").Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("expected Bearer tok-123, got: %q", got)
	}
}

func TestAuthHeader_apikey(t *testing.T) {
	httpSrv := mcptest.NewHTTP(t, mcptest.Upstream{})

	srv := newTestServer(t, server.Params{})
	sc := config.ServerConfig{
		Name:      "api",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "apikey", Header: "X-Api-Key", Token: "key-abc"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream with apikey auth failed: %v", err)
	}
	if got := httpSrv.Last("tools/list").Header.Get("X-Api-Key"); got != "key-abc" {
		t.Errorf("expected key-abc in X-Api-Key, got: %q", got)
	}
}

func TestAuthHeader_emptyToken_noHeader(t *testing.T) {
	httpSrv := mcptest.NewHTTP(t, mcptest.Upstream{})

	srv := newTestServer(t, server.Params{})
	sc := config.ServerConfig{
		Name:      "noauth",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "bearer", Token: ""},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream failed: %v", err)
	}
	if got := httpSrv.Last("tools/list").Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header with empty token, got: %q", got)
	}
}

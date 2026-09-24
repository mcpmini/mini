//go:build test

package invoke

import (
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestAttachAuthProvider_sharedRegistryReturnsSameProvider(t *testing.T) {
	dir := t.TempDir()
	registry := auth.NewProviderRegistry()
	sc := config.ServerConfig{
		Name: "srv",
		URL:  "https://mcp.example.com",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token"},
	}
	p := DialParams{Server: sc, ConfigDir: dir, Clock: clock.NewFake(), ProviderRegistry: registry}

	var cfg1, cfg2 transport.HTTPConnectionConfig
	if err := attachAuthProvider(&cfg1, p); err != nil {
		t.Fatal(err)
	}
	if err := attachAuthProvider(&cfg2, p); err != nil {
		t.Fatal(err)
	}
	if cfg1.AuthProvider != cfg2.AuthProvider {
		t.Error("shared registry must return the same provider instance for the same server")
	}
}

func TestAttachAuthProvider_customHeaderName(t *testing.T) {
	dir := t.TempDir()
	registry := auth.NewProviderRegistry()
	sc := config.ServerConfig{
		Name: "srv",
		URL:  "https://mcp.example.com",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: "http://localhost:1/token", Header: "X-Custom-Auth"},
	}
	p := DialParams{Server: sc, ConfigDir: dir, Clock: clock.NewFake(), ProviderRegistry: registry}

	var cfg transport.HTTPConnectionConfig
	if err := attachAuthProvider(&cfg, p); err != nil {
		t.Fatal(err)
	}
	if cfg.AuthHeaderName != "X-Custom-Auth" {
		t.Errorf("AuthHeaderName = %q, want X-Custom-Auth", cfg.AuthHeaderName)
	}
}

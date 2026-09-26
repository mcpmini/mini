//go:build test

package invoke

import (
	"testing"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestAttachAuthProvider_sameServer_sharesProvider(t *testing.T) {
	dir := t.TempDir()
	registry := provider.NewRegistry()
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

func TestAttachAuthProvider_customHeader_setsHeaderName(t *testing.T) {
	dir := t.TempDir()
	registry := provider.NewRegistry()
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

func TestAttachAuthProvider_staticAuthConfigured_leavesStaticHeader(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		token   string
		want    bool
	}{
		{name: "hand-set header", headers: map[string]string{"authorization": "Bearer pat"}},
		{name: "auth token", token: "pat"},
		{name: "no static auth", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := config.ServerConfig{
				Name: "srv", URL: "https://mcp.example.com", Headers: tc.headers,
				Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, Token: tc.token},
			}
			p := DialParams{Server: sc, ConfigDir: t.TempDir(), Clock: clock.NewFake(), ProviderRegistry: provider.NewRegistry()}
			cfg := transport.HTTPConnectionConfig{Headers: MergedHeaders(sc)}
			if err := attachAuthProvider(&cfg, p); err != nil {
				t.Fatal(err)
			}
			if got := cfg.AuthProvider != nil; got != tc.want {
				t.Errorf("provider attached = %v, want %v", got, tc.want)
			}
		})
	}
}

//go:build test

package invoke_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
)

func TestMergedHeaders_PlainHeader(t *testing.T) {
	sc := config.ServerConfig{Headers: map[string]string{"X-Foo": "bar"}}
	h := invoke.MergedHeaders(sc)
	if h["X-Foo"] != "bar" {
		t.Errorf("got %q", h["X-Foo"])
	}
}

func TestMergedHeaders_EnvExpansion(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret")
	sc := config.ServerConfig{Headers: map[string]string{"Authorization": "Bearer ${MY_TOKEN}"}}
	h := invoke.MergedHeaders(sc)
	if h["Authorization"] != "Bearer secret" {
		t.Errorf("got %q", h["Authorization"])
	}
}

func TestMergedHeaders_TrimsWhitespace(t *testing.T) {
	t.Setenv("API_KEY", "  tok  ")
	sc := config.ServerConfig{Headers: map[string]string{"X-Key": "  ${API_KEY}  "}}
	h := invoke.MergedHeaders(sc)
	if h["X-Key"] != "tok" {
		t.Errorf("got %q", h["X-Key"])
	}
}

func TestMergedHeaders_BearerAuth(t *testing.T) {
	t.Setenv("MY_TOKEN", "abc123")
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "bearer", Token: "${MY_TOKEN}"},
	}
	h := invoke.MergedHeaders(sc)
	if h["Authorization"] != "Bearer abc123" {
		t.Errorf("got %q", h["Authorization"])
	}
}

func TestMergedHeaders_APIKeyAuth(t *testing.T) {
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "apikey", Token: "rawkey", Header: "X-Api-Key"},
	}
	h := invoke.MergedHeaders(sc)
	if h["X-Api-Key"] != "rawkey" {
		t.Errorf("got %q", h["X-Api-Key"])
	}
}

func TestMergedHeaders_EmptyToken(t *testing.T) {
	sc := config.ServerConfig{
		Auth: &config.AuthConfig{Type: "bearer", Token: ""},
	}
	h := invoke.MergedHeaders(sc)
	if _, ok := h["Authorization"]; ok {
		t.Error("expected no Authorization header when token is empty")
	}
}

//go:build test

package server_test

import (
	"context"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddUpstream_detectsOAuthFrom401(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "needsauth", Transport: "http", URL: mcpSrv.URL})
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "needsauth")
	err := srv.AddUpstream(context.Background(), sc)
	if err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}
	if !strings.Contains(err.Error(), "mini auth needsauth") {
		t.Errorf("error should mention `mini auth needsauth`, got: %v", err)
	}

	if !config.IsOAuthDetected(dir, "needsauth") {
		t.Error("expected the oauth-detected marker to be written")
	}
	got := loadServerConfig(t, dir, "needsauth")
	if got.Auth == nil || got.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 merged in from the detected marker", got.Auth)
	}
}

func TestAddUpstream_doesNotOverwriteExistingAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "hasauth",
		Transport: "http",
		URL:       mcpSrv.URL,
		Auth: &config.AuthConfig{
			Type:  "apikey",
			Token: "secret",
		},
	})
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "hasauth")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "hasauth")
	if got.Auth == nil || got.Auth.Type != "apikey" {
		t.Errorf("Auth = %+v, existing apikey config was clobbered", got.Auth)
	}
}

func TestAddUpstream_bare401WithNoEvidenceDoesNotMarkOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain401", Transport: "http", URL: mcpSrv.URL})
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "plain401")
	err := srv.AddUpstream(context.Background(), sc)
	if err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}
	if strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Errorf("error should not claim OAuth is required, got: %v", err)
	}

	if config.IsOAuthDetected(dir, "plain401") {
		t.Error("a bare 401 with no PRM/header evidence must not write the oauth-detected marker")
	}
}

func TestAddUpstream_staticBearerHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "statictoken",
		Transport: "http",
		URL:       mcpSrv.URL,
		Headers:   map[string]string{"Authorization": "Bearer some-static-token"},
	})
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "statictoken")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	if config.IsOAuthDetected(dir, "statictoken") {
		t.Error("a server with a manually-configured Authorization header must never be marked oauth2 — RFC 6750 mandates the same Bearer challenge for an expired static token")
	}
}

func TestAddUpstream_customAuthHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "apikeyserver",
		Transport: "http",
		URL:       mcpSrv.URL,
		Headers:   map[string]string{"X-Api-Key": "some-static-key"},
	})
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "apikeyserver")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	if config.IsOAuthDetected(dir, "apikeyserver") {
		t.Error("a server with any manually-configured header must never be marked oauth2")
	}
}

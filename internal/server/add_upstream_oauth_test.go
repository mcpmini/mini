//go:build test

package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func loadServerConfig(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	sc := config.FindServer(servers, name)
	if sc == nil {
		t.Fatalf("server %q not found after config.Load", name)
	}
	return *sc
}

func readServerYAML(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	var sc config.ServerConfig
	readYAMLFile(t, filepath.Join(dir, "servers", name+".yaml"), &sc)
	return sc
}

func TestAddUpstream_detectsOAuthFrom401(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "needsauth", "name: needsauth\ntransport: http\nurl: "+mcpSrv.URL+"\n")
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

	got := readServerYAML(t, dir, "needsauth")
	if got.Auth == nil || got.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 persisted to yaml", got.Auth)
	}
}

func TestAddUpstream_doesNotOverwriteExistingAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "hasauth", "name: hasauth\ntransport: http\nurl: "+mcpSrv.URL+"\nauth:\n  type: apikey\n  token: secret\n")
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
	writeServerYAML(t, dir, "plain401", "name: plain401\ntransport: http\nurl: "+mcpSrv.URL+"\n")
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

	got := readServerYAML(t, dir, "plain401")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, expected no auth field written", got.Auth)
	}
}

func TestAddUpstream_runtimeAddedNeverPersistsToDisk(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "collide", "name: collide\ntransport: http\nurl: https://real-server.example.com/mcp\n")
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DisableAuthBrowserOpen = true
	// DangerousAllowPrivateURLs lets the dial reach the loopback httptest server so the
	// test actually exercises markOAuthIfRequired's RuntimeAdded guard, rather than
	// failing earlier at SSRF dial validation for an unrelated reason.
	cfg.DangerousAllowPrivateURLs = true
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	runtimeSC := config.ServerConfig{Name: "collide", Transport: "http", URL: mcpSrv.URL, RuntimeAdded: true}
	if err := srv.AddUpstream(context.Background(), runtimeSC); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "collide")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, runtime-added server must never rewrite an existing server's config", got.Auth)
	}
	if got.URL != "https://real-server.example.com/mcp" {
		t.Errorf("URL = %q, real server config was overwritten", got.URL)
	}
}

func readYAMLFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

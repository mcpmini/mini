//go:build test

package ops_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/transport"
)

func unauthorized(challenge string) error {
	return &transport.UnauthorizedError{WWWAuthenticate: challenge}
}

func prmServer(t *testing.T, withPRM bool) string {
	t.Helper()
	auth.UseLoopbackHTTPClient()
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !withPRM || r.URL.Path != "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"authorization_servers":["https://as.example.com"]}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func httpServer(url string) config.ServerConfig {
	return config.ServerConfig{Name: "svc", Transport: "http", URL: url}
}

func TestDetectOAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("bearer challenge records the marker", func(t *testing.T) {
		dir := tempDir(t)
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer("https://example.com/mcp"), ConnErr: unauthorized("Bearer")})
		if err != nil || !got {
			t.Fatalf("got (%v, %v), want (true, nil)", got, err)
		}
		if !config.IsOAuthDetected(dir, "svc") {
			t.Error("marker not written")
		}
	})

	t.Run("no challenge but a PRM document records the marker", func(t *testing.T) {
		dir := tempDir(t)
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer(prmServer(t, true)), ConnErr: unauthorized("")})
		if err != nil || !got {
			t.Fatalf("got (%v, %v), want (true, nil)", got, err)
		}
		if !config.IsOAuthDetected(dir, "svc") {
			t.Error("marker not written")
		}
	})

	t.Run("no challenge and no PRM document is not OAuth", func(t *testing.T) {
		dir := tempDir(t)
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer(prmServer(t, false)), ConnErr: unauthorized("")})
		if err != nil || got {
			t.Fatalf("got (%v, %v), want (false, nil)", got, err)
		}
		if config.IsOAuthDetected(dir, "svc") {
			t.Error("marker written without confirmation")
		}
	})

	t.Run("non-Bearer challenge is not OAuth", func(t *testing.T) {
		dir := tempDir(t)
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer("https://example.com/mcp"), ConnErr: unauthorized(`Basic realm="x"`)})
		if err != nil || got || config.IsOAuthDetected(dir, "svc") {
			t.Errorf("got (%v, %v), marker=%v, want (false, nil) and no marker", got, err, config.IsOAuthDetected(dir, "svc"))
		}
	})

	t.Run("a connection error that is not a 401 is not OAuth", func(t *testing.T) {
		dir := tempDir(t)
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer("https://example.com/mcp"), ConnErr: errors.New("connection refused")})
		if err != nil || got || config.IsOAuthDetected(dir, "svc") {
			t.Errorf("got (%v, %v), marker=%v, want (false, nil) and no marker", got, err, config.IsOAuthDetected(dir, "svc"))
		}
	})

	t.Run("already recorded short-circuits without probing or rewriting", func(t *testing.T) {
		dir := tempDir(t)
		if err := config.MarkOAuthDetected(dir, "svc"); err != nil {
			t.Fatal(err)
		}
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: dir, Server: httpServer("https://example.com/mcp"), ConnErr: errors.New("connection refused")})
		if err != nil || !got {
			t.Errorf("got (%v, %v), want (true, nil) from the recorded marker", got, err)
		}
	})

	t.Run("marker write failure is returned", func(t *testing.T) {
		notADir := filepath.Join(tempDir(t), "file")
		if err := os.WriteFile(notADir, nil, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ops.DetectOAuth(ctx, ops.DetectOAuthParams{ConfigDir: notADir, Server: httpServer("https://example.com/mcp"), ConnErr: unauthorized("Bearer")})
		if err == nil || got {
			t.Errorf("got (%v, %v), want (false, error)", got, err)
		}
	})
}

func TestDetectOAuth_ineligibleServers(t *testing.T) {
	cases := []struct {
		name string
		edit func(*config.ServerConfig)
	}{
		{"runtime added", func(sc *config.ServerConfig) { sc.RuntimeAdded = true }},
		{"auth already configured", func(sc *config.ServerConfig) { sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2} }},
		{"stdio transport", func(sc *config.ServerConfig) { sc.Transport = "stdio" }},
		{"static auth header", func(sc *config.ServerConfig) { sc.Headers = map[string]string{"Authorization": "Bearer tok"} }},
		{"custom-named credential header", func(sc *config.ServerConfig) { sc.Headers = map[string]string{"X-Api-Key": "key"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			sc := httpServer("https://example.com/mcp")
			tc.edit(&sc)
			got, err := ops.DetectOAuth(context.Background(), ops.DetectOAuthParams{ConfigDir: dir, Server: sc, ConnErr: unauthorized("Bearer")})
			if err != nil || got || config.IsOAuthDetected(dir, "svc") {
				t.Errorf("got (%v, %v), marker=%v, want (false, nil) and no marker", got, err, config.IsOAuthDetected(dir, "svc"))
			}
		})
	}
}

//go:build test

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func upstreamAnswering(t *testing.T, status int, challenge string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if challenge != "" {
			w.Header().Set("WWW-Authenticate", challenge)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestInitCommandDetectsOAuthOnImportedServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	src := filepath.Join(t.TempDir(), "claude.json")
	testutil.WriteFile(t, src, `{"mcpServers": {"svc": {"type": "http", "url": "`+url+`"}}}`)
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--from", src})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "1 still needs finishing") || !strings.Contains(out, "auth svc") {
		t.Errorf("init output = %q, want svc listed as needing a login", out)
	}
}

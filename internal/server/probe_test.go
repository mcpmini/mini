//go:build test

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestProbeServer_invalidConfigMakesNoRequest(t *testing.T) {
	var hit atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer upstream.Close()
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), "bad: [yaml\n")
	err := server.ProbeServer(
		context.Background(),
		dir,
		config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL},
	)
	if err == nil || !strings.Contains(err.Error(), "load config") || hit.Load() {
		t.Errorf("ProbeServer error=%v request hit=%v, want load-config error and no request", err, hit.Load())
	}
}

func TestProbeServer_recordsDetectedOAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	if err := server.ProbeServer(
		context.Background(),
		dir,
		config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL},
	); err == nil {
		t.Fatal("ProbeServer succeeded against a 401")
	}
	if !config.IsOAuthDetected(dir, "svc") {
		t.Error("the OAuth requirement wasn't recorded")
	}
}

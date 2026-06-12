//go:build test

package server_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const testDaemonToken = "0123456789abcdef0123456789abcdef"

func newAuthHTTPTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, logger, server.WithDaemonAuthToken(testDaemonToken))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func mcpPostAuth(t *testing.T, ts *httptest.Server, auth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(initRequest()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDaemonAuth_MCPRequiresBearerToken(t *testing.T) {
	ts := newAuthHTTPTestServer(t)
	cases := []struct {
		name string
		auth string
		want int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"no bearer prefix", testDaemonToken, http.StatusUnauthorized},
		{"correct token", "Bearer " + testDaemonToken, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := mcpPostAuth(t, ts, tc.auth)
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("got %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestDaemonAuth_HealthzUnauthenticated(t *testing.T) {
	ts := newAuthHTTPTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusOK)
}

//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
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

func postWithHost(t *testing.T, url, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/mcp", bytes.NewReader(initRequest()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
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

func TestHTTPServer_HostLoopbackCheck(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	cases := []struct {
		name string
		host string
		want int
	}{
		{"evil domain rejected", "evil.com", http.StatusForbidden},
		{"evil domain with port rejected", "evil.com:1234", http.StatusForbidden},
		{"loopback IP with port", "127.0.0.1:1234", http.StatusOK},
		{"ipv6 loopback", "[::1]:1234", http.StatusOK},
		{"localhost", "localhost:1234", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postWithHost(t, ts.URL, tc.host)
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("Host %q: got %d, want %d", tc.host, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestHTTPServer_SessionID(t *testing.T) {
	ts := httptest.NewServer(newTestServer(t))
	defer ts.Close()

	check := func(t *testing.T, id string, want int) {
		t.Helper()
		resp := mcpPost(t, ts, initRequest(), id)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("id=%q: got %d, want %d", id, resp.StatusCode, want)
		}
	}

	t.Run("valid UUID", func(t *testing.T) { check(t, "abcdef01-2345-6789-abcd-ef0123456789", 200) })
	t.Run("short ID", func(t *testing.T) { check(t, "a", 400) })
	t.Run("32 hyphens", func(t *testing.T) { check(t, "--------------------------------", 400) })
	t.Run("31 hex chars", func(t *testing.T) { check(t, "abcdef0123456789abcdef012345678", 400) })
	t.Run("32 hex chars", func(t *testing.T) { check(t, "abcdef0123456789abcdef0123456789", 200) })
}

func TestHTTPServer_BodyLimitRejected(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	oversized := make([]byte, 1<<20+1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	if body["error"] == nil {
		t.Errorf("expected JSON-RPC error for oversized body, got: %v", body)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/cmd/mini/importers"
)

func fakeUnauthenticatedMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo":   map[string]any{"name": "fake", "version": "0"}}})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"tools": []map[string]any{}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAdd(t *testing.T) {
	t.Run("stdio command creates server file", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "npx", "-y", "server-github"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "gh", &sc)
		if sc.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", sc.Command)
		}
		if len(sc.Args) != 2 || sc.Args[0] != "-y" {
			t.Errorf("Args = %v, want [-y server-github]", sc.Args)
		}
	})

	t.Run("--url flag creates http server", func(t *testing.T) {
		mcpSrv := fakeUnauthenticatedMCPServer(t)
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--url", mcpSrv.URL}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "gh", &sc)
		if sc.Transport != "http" {
			t.Errorf("Transport = %q, want 'http'", sc.Transport)
		}
		if sc.URL != mcpSrv.URL {
			t.Errorf("URL = %q, want %q", sc.URL, mcpSrv.URL)
		}
		if !strings.Contains(out.String(), "connected to gh") {
			t.Errorf("output = %q, want it to mention a successful connect", out.String())
		}
	})

	t.Run("connect failure unrelated to OAuth reports a plain note", func(t *testing.T) {
		mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		unreachableURL := mcpSrv.URL
		mcpSrv.Close() // closed before use: connection refused, not a 401

		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--url", unreachableURL}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		if !strings.Contains(out.String(), "note: could not connect to gh yet; run `mini test` to retry") {
			t.Errorf("output = %q, want the plain connect-failure note", out.String())
		}
		if strings.Contains(out.String(), "OAuth") {
			t.Errorf("output = %q, a non-OAuth connect failure should never mention OAuth", out.String())
		}
	})

	t.Run("--no-connect skips the connectivity probe", func(t *testing.T) {
		hit := false
		mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer mcpSrv.Close()

		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--url", mcpSrv.URL, "--no-connect"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		if hit {
			t.Error("--no-connect should skip the connectivity probe, but the server received a request")
		}
		if strings.Contains(out.String(), "connected") || strings.Contains(out.String(), "OAuth") {
			t.Errorf("output = %q, --no-connect should produce no connect/auth messages", out.String())
		}
	})

	t.Run("--header flag is stored", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--url", "https://example.com", "--header", "Authorization=Bearer tok", "--no-connect"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "svc", &sc)
		if sc.Headers["Authorization"] != "Bearer tok" {
			t.Errorf("Header Authorization = %q, want 'Bearer tok'", sc.Headers["Authorization"])
		}
	})

	t.Run("auto-authorize failure does not exit the process", func(t *testing.T) {
		// This server 401s with a Bearer challenge on a loopback URL, which SSRF validation
		// rejects during OAuth endpoint discovery — a real, reachable auto-authorize failure.
		oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer oauthSrv.Close()

		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"myserver", "--url", oauthSrv.URL}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		if !strings.Contains(out.String(), "requires OAuth authorization") {
			t.Errorf("output = %q, want it to mention OAuth is required", out.String())
		}
		if !strings.Contains(out.String(), "run `mini auth myserver` to retry") {
			t.Errorf("output = %q, want a graceful failure message pointing at manual retry", out.String())
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "myserver", &sc)
		if sc.URL != oauthSrv.URL {
			t.Errorf("URL = %q, server config should still have been written despite auto-authorize failing", sc.URL)
		}
	})

	t.Run("--protected flag marks tool", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--protected", "delete_everything", "run"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc importers.ServerYAML
		readServerYAML(t, dir, "svc", &sc)
		if sc.Permissions == nil || len(sc.Permissions.Protected) != 1 || sc.Permissions.Protected[0] != "delete_everything" {
			t.Errorf("Protected = %v, want [delete_everything]", sc.Permissions)
		}
	})

	t.Run("missing name returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error when no name given")
		}
	})

	t.Run("name but no url and no command returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{"myserver"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error when no url or command given")
		}
	})

	t.Run("invalid server name returns error", func(t *testing.T) {
		dir := t.TempDir()
		err := runAdd(dir, []string{"bad name!", "npx"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})
}

func TestRunRemove(t *testing.T) {
	t.Run("removes existing server", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		runAdd(dir, []string{"myserver", "run"}, &out) //nolint:errcheck
		out.Reset()

		if err := runRemove(dir, []string{"myserver"}, &out); err != nil {
			t.Fatalf("runRemove: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "myserver.yaml")); err == nil {
			t.Fatal("server file still exists after remove")
		}
		if !strings.Contains(out.String(), "removed myserver") {
			t.Errorf("output = %q, want 'removed myserver'", out.String())
		}
	})

	t.Run("no args returns error", func(t *testing.T) {
		dir := t.TempDir()
		if err := runRemove(dir, []string{}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error when no name given")
		}
	})

	t.Run("non-existent server returns error", func(t *testing.T) {
		dir := t.TempDir()
		if err := runRemove(dir, []string{"ghost"}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected error removing non-existent server")
		}
	})
}

func readServerYAML(t *testing.T, configDir, name string, out any) {
	t.Helper()
	path := filepath.Join(configDir, "servers", name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
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

func runAdd(configDir string, args []string, out *bytes.Buffer) error {
	cmd := newAddCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

func TestRunAdd(t *testing.T) {
	t.Run("a name with neither a URL nor a command is a usage error, not a crash", func(t *testing.T) {
		dir := t.TempDir()
		if err := addNamedServer(dir, serverFlags{name: "svc"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "provide --url or a command") {
			t.Fatalf("addNamedServer = %v, want the usage error", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "servers", "svc.yaml")); err == nil {
			t.Error("servers/svc.yaml was written for a server with no URL or command")
		}
	})

	t.Run("stdio command creates server file", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--", "npx", "-y", "server-github"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc config.ServerConfig
		readServerYAML(t, dir, "gh", &sc)
		if sc.Command != "npx" {
			t.Errorf("Command = %q, want 'npx'", sc.Command)
		}
		if len(sc.Args) != 2 || sc.Args[0] != "-y" {
			t.Errorf("Args = %v, want [-y server-github]", sc.Args)
		}
	})

	t.Run("reports the server file, bundled projection and default permissions it wrote", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--", "npx", "-y", "server-github"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		printed := out.String()
		serverPath := filepath.Join(dir, "servers", "gh.yaml")
		for _, want := range []string{
			"added gh → " + serverPath,
			"installed default projection → " + filepath.Join(dir, "servers", "gh.proj.yaml"),
			"applied default permissions → " + serverPath,
		} {
			if !strings.Contains(printed, want) {
				t.Errorf("output %q is missing %q", printed, want)
			}
		}
	})

	t.Run("stdio child flags are stored unchanged", func(t *testing.T) {
		dir := t.TempDir()
		args := []string{"svc", "--", "/usr/bin/printf", "-h", "--config", "child-value"}
		if err := runAdd(dir, args, &bytes.Buffer{}); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc config.ServerConfig
		readServerYAML(t, dir, "svc", &sc)
		want := []string{"-h", "--config", "child-value"}
		if !slices.Equal(sc.Args, want) {
			t.Errorf("Args = %v, want %v", sc.Args, want)
		}
	})

	t.Run("--url flag creates http server", func(t *testing.T) {
		mcpSrv := fakeUnauthenticatedMCPServer(t)
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runAdd(dir, []string{"gh", "--url", mcpSrv.URL}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc config.ServerConfig
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

	t.Run("connect failure unrelated to auth reports a plain note", func(t *testing.T) {
		mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		unreachableURL := mcpSrv.URL
		mcpSrv.Close()

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
		var sc config.ServerConfig
		readServerYAML(t, dir, "svc", &sc)
		if sc.Headers["Authorization"] != "Bearer tok" {
			t.Errorf("Header Authorization = %q, want 'Bearer tok'", sc.Headers["Authorization"])
		}
	})

	t.Run("--header suppresses auto-authorize even for a bundled-vendor URL", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--url", "https://slack.com/mcp", "--header", "Authorization=Bearer xoxb-test"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		if strings.Contains(out.String(), "OAuth") {
			t.Errorf("output = %q, an explicit --header should suppress the automatic OAuth flow for a known vendor", out.String())
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
		var sc config.ServerConfig
		readServerYAML(t, dir, "myserver", &sc)
		if sc.URL != oauthSrv.URL {
			t.Errorf("URL = %q, server config should still have been written despite auto-authorize failing", sc.URL)
		}
	})

	t.Run("warns instead of silently skipping when config reload fails", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "servers"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "servers", "broken.yaml"), []byte("not: valid: yaml: ["), 0644); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if err := runAdd(dir, []string{"svc", "--url", "https://example.com"}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		if !strings.Contains(out.String(), "warning:") {
			t.Errorf("output = %q, want a warning when config reload fails instead of silence", out.String())
		}
	})

	t.Run("--protected flag marks tool", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		args := []string{"svc", "--protected", "delete_everything", "--", "run"}
		if err := runAdd(dir, args, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}
		var sc config.ServerConfig
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
		err := runAdd(dir, []string{"bad name!", "--", "npx"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid server name")
		}
	})

	t.Run("a configured name is refused with how to replace it, and its file is untouched", func(t *testing.T) {
		dir := t.TempDir()
		if err := runAdd(dir, []string{"svc", "--", "original"}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		before := readFileString(t, filepath.Join(dir, "servers", "svc.yaml"))

		err := runAdd(dir, []string{"svc", "--", "replacement"}, &bytes.Buffer{})

		if err == nil || !strings.Contains(err.Error(), "svc is already configured; run `mini rm svc` first to replace it") {
			t.Errorf("err = %v, want the already-configured error naming mini rm", err)
		}
		if after := readFileString(t, filepath.Join(dir, "servers", "svc.yaml")); after != before {
			t.Errorf("svc.yaml changed from %q to %q", before, after)
		}
	})

	t.Run("multiple import modes return error", func(t *testing.T) {
		err := runAdd(t.TempDir(), []string{"--from-claude", "a.json", "--from-cursor", "b.json"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for multiple import modes")
		}
	})

	t.Run("url and stdio command return error", func(t *testing.T) {
		err := runAdd(t.TempDir(), []string{"svc", "--url", "https://example.com", "--", "command"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for mixed HTTP and stdio modes")
		}
	})
}

func TestRunAddImport(t *testing.T) {
	sources := []struct {
		flag, file, config, tip string
	}{
		{"--from-claude", "claude.json", `{"mcpServers":{"svc":{"command":"run"}}}`, headersTip},
		{"--from-cursor", "mcp.json", `{"mcpServers":{"svc":{"command":"run"}}}`, headersTip},
		{"--from-codex", "config.toml", "[mcp_servers.svc]\ncommand = \"run\"\n", envTip},
		{"--from-gemini", "settings.json", `{"mcpServers":{"svc":{"command":"run"}}}`, headersTip},
		{"--from-openclaw", "openclaw.json", `{"mcp":{"servers":{"svc":{"command":"run"}}}}`, envTip},
	}
	for _, src := range sources {
		t.Run(src.flag+" adds the server and prints the tip", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(t.TempDir(), src.file)
			writeImportSource(t, path, src.config)
			var out bytes.Buffer

			if err := runAdd(dir, []string{src.flag, path}, &out); err != nil {
				t.Fatalf("runAdd: %v", err)
			}

			var sc config.ServerConfig
			readServerYAML(t, dir, "svc", &sc)
			if sc.Command != "run" || !strings.Contains(out.String(), src.tip) {
				t.Errorf("command = %q, output = %q; want run and %q", sc.Command, out.String(), src.tip)
			}
		})
	}

	t.Run("a rerun keeps the configured server, reports it and still succeeds", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(t.TempDir(), "claude.json")
		writeImportSource(t, path, `{"mcpServers":{"svc":{"command":"run"}}}`)
		if err := runAdd(dir, []string{"--from-claude", path}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		writeImportSource(t, path, `{"mcpServers":{"svc":{"command":"other"}}}`)
		var out bytes.Buffer

		if err := runAdd(dir, []string{"--from-claude", path}, &out); err != nil {
			t.Fatalf("rerun: %v", err)
		}

		var sc config.ServerConfig
		readServerYAML(t, dir, "svc", &sc)
		if sc.Command != "run" {
			t.Errorf("command = %q, want the configured run kept", sc.Command)
		}
		if want := path + ": svc not imported, mini's config has a different command"; !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
		if strings.Contains(out.String(), "tip:") {
			t.Errorf("output = %q, want no tip when nothing was added", out.String())
		}
	})

	t.Run("a config with no servers says so", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		writeImportSource(t, path, `{}`)
		var out bytes.Buffer

		if err := runAdd(t.TempDir(), []string{"--from-gemini", path}, &out); err != nil {
			t.Fatalf("runAdd: %v", err)
		}

		if want := "no MCP servers found in " + path; !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("a server that fails to add makes the import fail", func(t *testing.T) {
		notADir := filepath.Join(t.TempDir(), "file")
		writeImportSource(t, notADir, "")
		path := filepath.Join(t.TempDir(), "claude.json")
		writeImportSource(t, path, `{"mcpServers":{"svc":{"command":"run"}}}`)

		err := runAdd(notADir, []string{"--from-claude", path}, &bytes.Buffer{})

		if want := "1 of 1 servers in " + path + " could not be added"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	})

	t.Run("an unreadable config is an error", func(t *testing.T) {
		if err := runAdd(t.TempDir(), []string{"--from-codex", filepath.Join(t.TempDir(), "missing.toml")}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected an error for a missing config")
		}
	})
}

func TestProbeConnectionInvalidConfigMakesNoRequest(t *testing.T) {
	var hit atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer upstream.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bad: [yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := probeConnection(context.Background(), dir, config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL})
	if err == nil || !strings.Contains(err.Error(), "load config") || hit.Load() {
		t.Errorf("probeConnection error=%v request hit=%v, want load-config error and no request", err, hit.Load())
	}
}

func TestConnectAndAuthorizeIfNeeded_onlyStaticAuthSkipsLogin(t *testing.T) {
	// A loopback URL fails SSRF validation during OAuth endpoint discovery, so an attempted
	// login stops before any browser opens.
	loopback := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(loopback.Close)
	tests := []struct {
		name      string
		header    string
		wantLogin bool
	}{
		{"unrelated header still logs in", "X-Tenant: acme", true},
		{"static auth header skips login", "Authorization: Bearer static-token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			yaml := "transport: http\nurl: " + loopback.URL + "\nheaders:\n  " + tt.header + "\nauth:\n  type: oauth2\n"
			if err := os.MkdirAll(filepath.Join(dir, "servers"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "servers", "svc.yaml"), []byte(yaml), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			connectAndAuthorizeIfNeeded(dir, "svc", &out)
			if got := strings.Contains(out.String(), "requires OAuth authorization"); got != tt.wantLogin {
				t.Errorf("login attempted = %v, want %v; output = %q", got, tt.wantLogin, out.String())
			}
		})
	}
}

func TestAuthUndiscovered(t *testing.T) {
	tests := []struct {
		name string
		sc   config.ServerConfig
		want bool
	}{
		{"http without auth", config.ServerConfig{Transport: "http", URL: "https://x.example/mcp"}, true},
		{"http with auth", config.ServerConfig{Transport: "http", URL: "https://x.example/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}, false},
		{"stdio", config.ServerConfig{Command: "x"}, false},
	}
	for _, tt := range tests {
		if got := authUndiscovered(tt.sc); got != tt.want {
			t.Errorf("%s: authUndiscovered = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRunRemove(t *testing.T) {
	t.Run("removes existing server", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		runAdd(dir, []string{"myserver", "--", "run"}, &out) //nolint:errcheck
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

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

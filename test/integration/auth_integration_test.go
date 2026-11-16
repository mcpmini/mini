//go:build integration

package integration_test

import (
	"bufio"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestIntegrationAuth_bearerTokenSentToUpstream(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Auth: &config.AuthConfig{
			Type:  "bearer",
			Token: "my-secret-token",
		},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got := f.srv.Last("tools/call").Header.Get("Authorization"); got != "Bearer my-secret-token" {
		t.Errorf("expected Bearer my-secret-token, got %q", got)
	}
}

func TestIntegrationAuth_apiKeySentToUpstream(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Auth: &config.AuthConfig{
			Type:   "apikey",
			Token:  "my-api-key",
			Header: "X-Api-Key",
		},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got := f.srv.Last("tools/call").Header.Get("X-Api-Key"); got != "my-api-key" {
		t.Errorf("expected my-api-key, got %q", got)
	}
}

func TestIntegrationAuth_staticHeaderForwarded(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "sse",
		URL:       f.srv.URL,
		Headers:   map[string]string{"X-Custom-Key": "custom-value"},
	})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	if got := f.srv.Last("tools/call").Header.Get("X-Custom-Key"); got != "custom-value" {
		t.Errorf("expected custom-value, got %q", got)
	}
}

func TestIntegrationAuth_noTokenNoAuthHeader(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "svc", Transport: "sse", URL: f.srv.URL})

	client := startServer(t, cfg)
	client.execTool("svc", "get_item", nil)

	call := f.srv.Last("tools/call")
	if call.Method == "" {
		t.Fatal("the upstream never received tools/call")
	}
	if got := call.Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header without auth config, got %q", got)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForLine(t *testing.T, lines <-chan string, prefix string) string {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatalf("output ended before a line starting %q", prefix)
			}
			if strings.HasPrefix(line, prefix) {
				return line
			}
		case <-timeout:
			t.Fatalf("no output line starting %q within 30s", prefix)
		}
	}
}

func TestIntegrationAuth_miniAuthLogsInThroughTheRunningDaemon(t *testing.T) {
	t.Parallel()
	cfg := oauthServerConfig(t)
	startHealthyDaemon(t, cfg)

	authorized, err := runMiniAuth(t, cfg)

	if err != nil || !strings.HasPrefix(authorized, "authorized svc (expires ") {
		t.Fatalf("mini auth = %v, printed %q; want it authorized", err, authorized)
	}
	if tools := connect(t, cfg).listTools(); !strings.Contains(tools, "svc__getData") {
		t.Errorf("tools/list right after mini auth = %s, want svc's getData", tools)
	}
}

func TestIntegrationAuth_miniAuthWithoutADaemonLogsInWithoutStartingOne(t *testing.T) {
	t.Parallel()
	cfg := oauthServerConfig(t)
	t.Cleanup(func() { reapDaemons(cfg) }) // in case mini auth starts one in its own session

	authorized, err := runMiniAuth(t, cfg)

	if err != nil || !strings.HasPrefix(authorized, "authorized svc (expires ") {
		t.Fatalf("mini auth = %v, printed %q; want it authorized", err, authorized)
	}
	if daemonHealthy(cfg) {
		t.Error("a daemon is running after mini auth; want none started")
	}
}

func oauthServerConfig(t *testing.T) string {
	t.Helper()
	tokenServer := authtest.NewTokenServer(t)
	cfg := shortConfigDir(t)
	settings := config.DefaultConfig()
	settings.DisableAuthBrowserOpen = true
	configtest.WriteConfig(t, cfg, settings)
	oauth := tokenServer.AuthConfig()
	oauth.CallbackPort = freeLoopbackPort(t)
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       authtest.NewOAuthMCPServer(t, authtest.OAuthMCPServerParams{AuthServerURL: tokenServer.Srv.URL}).URL,
		Auth:      oauth,
	})
	return cfg
}

// runMiniAuth completes the browser step and returns the authorized line and mini auth's exit.
func runMiniAuth(t *testing.T, cfg string) (string, error) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", cfg, "auth", "svc")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() }) //nolint:errcheck // it has exited unless the test failed first
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		for scanner := bufio.NewScanner(stdout); scanner.Scan(); {
			lines <- scanner.Text()
		}
	}()
	authtest.CompleteAuthorization(t, waitForLine(t, lines, "http"), "test-auth-code")
	authorized := waitForLine(t, lines, "authorized")
	for range lines { // Wait closes the pipe, so the output is read to its end first
	}
	return authorized, cmd.Wait()
}

//go:build integration

package integration_test

import (
	"bufio"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/testutil"
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

// oauthProtectedUpstream challenges requests without tokenServer's access token, naming
// tokenServer as its authorization server, and serves a getData tool to requests with it.
func oauthProtectedUpstream(t *testing.T, tokenServer *authtest.TokenServer) string {
	t.Helper()
	mcp := mcptest.NewHandler(mcptest.Upstream{Tools: []mcptest.Tool{{Name: "getData"}}})
	upstream := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			testutil.WriteJSON(w, http.StatusOK, authtest.ProtectedResource{
				Resource: "http://" + r.Host, AuthorizationServers: []string{tokenServer.Srv.URL},
			})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tokenServer.AccessToken {
			authtest.Challenge{ResourceMetadata: "http://" + r.Host + "/.well-known/oauth-protected-resource"}.Write(w)
			return
		}
		mcp.ServeHTTP(w, r)
	})
	return upstream.URL
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

func TestIntegrationAuth_miniAuthStartsTheDaemonWhichConnectsTheServer(t *testing.T) {
	t.Parallel()
	tokenServer := authtest.NewTokenServer(t)
	cfg := shortConfigDir(t)
	t.Cleanup(func() { reapDaemons(cfg) }) // the daemon mini auth starts runs in its own session
	settings := config.DefaultConfig()
	settings.DisableAuthBrowserOpen = true
	configtest.WriteConfig(t, cfg, settings)
	oauth := tokenServer.AuthConfig()
	oauth.CallbackPort = freeLoopbackPort(t)
	configtest.WriteServer(t, cfg, config.ServerConfig{
		Name: "svc", Transport: "http", URL: oauthProtectedUpstream(t, tokenServer), Auth: oauth,
	})
	if daemonHealthy(cfg) {
		t.Fatal("a daemon is running before mini auth")
	}
	cmd := exec.Command(miniBin, "--config", cfg, "auth", "svc")
	var stderr strings.Builder
	cmd.Stderr = &stderr
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
	if err := cmd.Wait(); err != nil || !strings.HasPrefix(authorized, "authorized svc (expires ") {
		t.Fatalf("mini auth = %v, printed %q, stderr %q; want it authorized", err, authorized, stderr.String())
	}
	if !daemonHealthy(cfg) {
		t.Fatal("no daemon running after mini auth; want the one it started for the login")
	}
	if tools := connect(t, cfg).listTools(); !strings.Contains(tools, "svc__getData") {
		t.Errorf("tools/list right after mini auth = %s, want svc's getData", tools)
	}
}

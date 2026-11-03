//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/mcptest"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func fakeOAuthMCPServer(t *testing.T, validToken string, tools []mcptest.Tool) *mcptest.Server {
	t.Helper()
	return mcptest.NewHTTP(t, mcptest.Upstream{
		Tools:     tools,
		Intercept: serveOAuthMetadata,
		Authorize: func(r mcptest.Request) bool { return r.Header.Get("Authorization") == "Bearer "+validToken },
	})
}

// fakeTokenServer returns an OAuth2 token server that issues the given access token.
func fakeTokenServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	tokens := authtest.NewTokenServer(t)
	tokens.AccessToken = accessToken
	return tokens.Srv
}

type tokenGate struct {
	*mcptest.Server
	mu                sync.Mutex
	accepted          string
	rejectedWithToken atomic.Int32
}

func newTokenGate(t *testing.T) *tokenGate {
	t.Helper()
	g := &tokenGate{}
	g.Server = mcptest.NewHTTP(t, mcptest.Upstream{
		Tools:     []mcptest.Tool{{Name: "getData", Description: "get data"}},
		Intercept: serveOAuthMetadata,
		Authorize: g.authorize,
	})
	return g
}

func (g *tokenGate) authorize(r mcptest.Request) bool {
	g.mu.Lock()
	accepted := g.accepted
	g.mu.Unlock()
	sent := r.Header.Get("Authorization")
	if accepted != "" && sent == "Bearer "+accepted {
		return true
	}
	if sent != "" {
		g.rejectedWithToken.Add(1)
	}
	return false
}

func (g *tokenGate) accept(token string) {
	g.mu.Lock()
	g.accepted = token
	g.mu.Unlock()
	g.rejectedWithToken.Store(0)
}

func completeStartAuth(t *testing.T, srv *server.Server, name string) {
	t.Helper()
	authtest.CompleteAuthorization(t, startAuth(t, srv, name), "test-code")
}

func startAuth(t *testing.T, srv *server.Server, name string) string {
	t.Helper()
	result := parseEnvelope(
		t,
		toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": name}))),
	)
	url, _ := result["url"].(string)
	if url == "" {
		t.Fatalf("start_auth = %v, want an authorization URL", result)
	}
	return url
}

type newOAuthServerParams struct {
	T          *testing.T
	ConfigDir  string
	ServerName string
	TokenURL   string
	MCPURL     string
}

func newOAuthServer(params newOAuthServerParams) *server.Server {
	params.T.Helper()
	configtest.WriteServer(params.T, params.ConfigDir, config.ServerConfig{
		Name:      params.ServerName,
		Transport: "http",
		URL:       params.MCPURL,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			Issuer:   params.MCPURL,
			ClientID: "test-client",
			AuthURL:  params.TokenURL + "/authorize",
			TokenURL: params.TokenURL + "/token",
		},
	})
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	return newTestServer(params.T, server.Params{Config: cfg, ConfigDir: params.ConfigDir})
}

func waitForServerConnected(t *testing.T, srv *server.Server, svcName string) {
	t.Helper()
	for range 100 {
		statusText := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
		var status map[string]any
		if err := json.Unmarshal([]byte(statusText), &status); err == nil {
			if servers, ok := status["servers"].(map[string]any); ok {
				if _, connected := servers[svcName]; connected {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to connect after OAuth flow", svcName)
}

func TestStartAuth_e2e_connectsAfterOAuthFlow(t *testing.T) {
	const accessToken = "e2e-valid-token"
	tokenSrv := fakeTokenServer(t, accessToken)
	mcpSrv := fakeOAuthMCPServer(t, accessToken, []mcptest.Tool{
		{Name: "getData", Description: "get data"},
	})
	srv := newOAuthServer(
		newOAuthServerParams{
			T:          t,
			ConfigDir:  t.TempDir(),
			ServerName: "protected",
			TokenURL:   tokenSrv.URL,
			MCPURL:     mcpSrv.URL,
		},
	)
	completeStartAuth(t, srv, "protected")

	waitForServerConnected(t, srv, "protected")
}

func TestStartAuth_opensServerBrowserCommandWithAuthURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: the browser command uses shell redirection")
	}
	dir := t.TempDir()
	openedPath := filepath.Join(dir, "opened-url")
	tokenSrv := fakeTokenServer(t, "unused-token")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "protected",
		Transport: "http",
		URL:       tokenSrv.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:       "oauth2",
			Issuer:     tokenSrv.URL,
			ClientID:   "test-client",
			AuthURL:    tokenSrv.URL + "/authorize",
			TokenURL:   tokenSrv.URL + "/token",
			BrowserCmd: "printf %s > " + openedPath,
		},
	})
	cfg := config.DefaultConfig()
	cfg.BrowserCommand = "false"
	srv := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})

	authURL := startAuth(t, srv, "protected")

	if opened := waitForFileContent(t, openedPath); opened != authURL {
		t.Errorf("browser opened %q, want the start_auth URL %q", opened, authURL)
	}
}

func waitForFileContent(t *testing.T, path string) string {
	t.Helper()
	for range 100 {
		data, err := os.ReadFile(path) //fileiolint:allow poll for output from the browser process
		if err == nil &&
			len(data) > 0 {
			return string(data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func TestStartAuth_e2e_toolsAccessibleAfterAuth(t *testing.T) {
	const accessToken = "e2e-tools-token"
	tokenSrv := fakeTokenServer(t, accessToken)
	mcpSrv := fakeOAuthMCPServer(t, accessToken, []mcptest.Tool{
		{Name: "search", Description: "search things"},
		{Name: "create", Description: "create thing"},
	})
	srv := newOAuthServer(
		newOAuthServerParams{
			T:          t,
			ConfigDir:  t.TempDir(),
			ServerName: "mysvc",
			TokenURL:   tokenSrv.URL,
			MCPURL:     mcpSrv.URL,
		},
	)
	completeStartAuth(t, srv, "mysvc")

	for range 100 {
		listText := toolListJSON(t, serve(t, srv, rpc("tools/list", nil)))
		if strings.Contains(listText, `"mysvc__search"`) && strings.Contains(listText, `"mysvc__create"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for tools to be accessible after OAuth flow")
}

func loadServerConfig(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	sc, err := config.LoadServer(dir, name)
	if err != nil {
		t.Fatalf("config.LoadServer: %v", err)
	}
	return sc
}

func readServerYAML(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	var sc config.ServerConfig
	readYAMLFile(t, filepath.Join(dir, "servers", name+".yaml"), &sc)
	return sc
}

func readYAMLFile(t *testing.T, path string, out any) {
	t.Helper()
	data := testutil.ReadFile(t, path)
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

func TestStartAuth_e2e_withStaleToken_browserTokenUsedOnFirstRequest(t *testing.T) {
	dir := t.TempDir()
	upstream := newTokenGate(t)
	tokens := authtest.NewTokenServer(t)
	tokens.AccessToken = "browser-token"
	tokens.Status.Store(http.StatusServiceUnavailable)
	mini := newOAuthServer(newOAuthServerParams{
		T: t, ConfigDir: dir, ServerName: "srv", TokenURL: tokens.Srv.URL, MCPURL: upstream.URL,
	})
	sc := loadServerConfig(t, dir, "srv")
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: "srv", Server: sc, Token: &oauth2.Token{AccessToken: "stale-token"},
	})
	if err := mini.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("initial dial with the stale token should fail, leaving a provider registered")
	}
	tokens.Status.Store(http.StatusOK)
	upstream.accept("browser-token")

	completeStartAuth(t, mini, "srv")
	waitForServerConnected(t, mini, "srv")
	serve(t, mini, rpc("tools/list", nil))

	if n := upstream.rejectedWithToken.Load(); n > 0 {
		t.Errorf("browser token caused %d unexpected 401 responses after auth, want 0", n)
	}
}

func serveOAuthMetadata(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/.well-known/oauth-authorization-server" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"issuer":                           "http://" + r.Host,
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	return true
}

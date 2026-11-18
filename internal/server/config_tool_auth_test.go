//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func newServerWithDir(t *testing.T, configDir string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	return newTestServer(t, server.Params{Config: cfg, ConfigDir: configDir})
}

func configureResult(t *testing.T, srv *server.Server, args map[string]any) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("config", args))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result, got: %s", text)
	}
	return result
}

func writeOAuthServer(t *testing.T, dir string) config.ServerConfig {
	t.Helper()
	sc := config.ServerConfig{
		Name: "myserver", Transport: "http", URL: "https://mcp.example.com/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
	}
	configtest.WriteServer(t, dir, sc)
	return sc
}

func TestAuthStatus_noToken_returnsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	writeOAuthServer(t, dir)
	srv := newServerWithDir(t, dir)

	result := configureResult(t, srv, map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})

	if result["authorized"] != false {
		t.Errorf("expected authorized=false for missing token, got: %v", result["authorized"])
	}
	if result["server"] != "myserver" {
		t.Errorf("expected server=myserver, got: %v", result["server"])
	}
}

func TestAuthStatus_validToken_returnsAuthorized(t *testing.T) {
	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "myserver",
		Server:     writeOAuthServer(t, dir),
		Token: &oauth2.Token{
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})

	result := configureResult(t, newServerWithDir(t, dir), map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})
	if result["authorized"] != true {
		t.Errorf("expected authorized=true for valid token, got: %v", result["authorized"])
	}
	if result["expires"] == nil {
		t.Error("expected non-nil expires for token with expiry")
	}
}

func TestAuthStatus_loginForAnotherURL_returnsUnauthorizedWithReason(t *testing.T) {
	dir := t.TempDir()
	madeFor := writeOAuthServer(t, dir)
	madeFor.URL = "https://old.example.com/mcp"
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: "myserver", Server: madeFor,
		Token: &oauth2.Token{AccessToken: "t", Expiry: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
	})

	result := configureResult(t, newServerWithDir(t, dir), map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})
	if result["authorized"] != false {
		t.Errorf("authorized = %v, want false for a login made for another URL", result["authorized"])
	}
	if reason, _ := result["reason"].(string); !strings.Contains(reason, "server URL changed") {
		t.Errorf("reason = %q, want it to say the server URL changed", reason)
	}
}

func TestAuthStatus_expiredToken_returnsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "myserver",
		Server:     writeOAuthServer(t, dir),
		Token: &oauth2.Token{
			AccessToken: "old-token",
			TokenType:   "Bearer",
			Expiry:      time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})

	result := configureResult(t, newServerWithDir(t, dir), map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})
	if result["authorized"] != false {
		t.Errorf("expected authorized=false for expired token, got: %v", result["authorized"])
	}
}

func TestAuthStatus_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "auth_status",
		"server": "bad name!",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for invalid server name, got: %v", result)
	}
}

func TestStartAuth_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "bad name!",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for invalid server name, got: %v", result)
	}
}

func TestStartAuth_serverNotFound_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "nonexistent",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for missing server, got: %v", result)
	}
}

func TestStartAuth_noOAuthConfig_returnsError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain", Command: "echo hello"})
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "plain",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for server without oauth2, got: %v", result)
	}
}

func TestStartAuth_loadsTheNamedServerWhenAnotherServerFileIsBroken(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain", Command: "echo hello"})
	testutil.WriteFile(t, config.ServerPath(dir, "other"), "transport: http\nurl: [unfinished\n")
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "plain"}))
	if text := toolResultText(t, resp); !strings.Contains(text, `"plain" does not have oauth2 auth configured`) {
		t.Errorf("start_auth plain = %q, want the OAuth check on plain's own config", text)
	}
}

func TestServeLogin_serverThatCantLogInIsAJSONError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain", Command: "echo hello"})
	_, ts := newHTTPTestServer(t, server.Params{ConfigDir: dir})
	for _, name := range []string{"nonexistent", "plain", "bad name!"} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"server": name})
			if err != nil {
				t.Fatal(err)
			}

			resp, err := ts.Client().Post(ts.URL+"/login", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			var result map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
			}
			if message, _ := result["message"].(string); decodeErr != nil || result["error"] == nil || message == "" {
				t.Errorf("body = %v (decode error %v), want a JSON error with a message", result, decodeErr)
			}
		})
	}
}

func TestStartAuth_removeServerWhileTheLoginFinishes_leavesNoTokenOrTools(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(*testing.T, *heldStep) *oauthEnv
	}{
		{name: "removed during the token exchange", arrange: loginHeldAtTokenExchange},
		{name: "removed during the reconnect with the new token", arrange: loginHeldAtReconnect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := newHeldStep(t)
			e := tc.arrange(t, step)
			callbackAnswered := deliverLoginCallback(t, startAuth(t, e.srv, "svc"))
			step.awaitReached("the login to reach the held step")

			assertRemoveOk(t, e.srv, "svc")
			step.release()
			testutil.WaitForReceive(t, callbackAnswered, "the login page to answer")
			e.srv.WaitForLogins()

			e.assertNoTokenOrTools()
			assertNoFile(t, "svc's server file", config.ServerPath(e.dir, "svc"))
		})
	}
}

func TestClose_duringTheReconnectOfAFinishedLogin_returnsAndLeavesNoTools(t *testing.T) {
	step := newHeldStep(t)
	e := loginHeldAtReconnect(t, step)
	authtest.CompleteAuthorization(t, startAuth(t, e.srv, "svc"), "test-code")
	step.awaitReached("the login's reconnect to reach initialize")

	closed := testutil.RunInBackground(func() error { e.srv.Close(); return nil })
	testutil.WaitForReceive(t, closed, "Close to return while the reconnect is held")

	if n := e.srv.ToolCount("svc"); n != 0 {
		t.Errorf("tools after Close = %d, want 0", n)
	}
}

func deliverLoginCallback(t *testing.T, authURL string) <-chan error {
	callback := authtest.CallbackURL(t, authURL, "test-code")
	// the login page answers only after the token exchange, which these tests hold
	return testutil.RunInBackground(func() error {
		authtest.DeliverCallback(callback)
		return nil
	})
}

func loginHeldAtTokenExchange(t *testing.T, step *heldStep) *oauthEnv {
	e := newOAuthEnv(t, func(loginToken string) *mcptest.Server {
		return fakeOAuthMCPServer(t, loginToken, pingTools)
	})
	holdNextTokenRequest(t, e.tokens, step)
	return e
}

func loginHeldAtReconnect(t *testing.T, step *heldStep) *oauthEnv {
	return newOAuthEnv(t, func(loginToken string) *mcptest.Server {
		return mcptest.NewHTTP(t, mcptest.Upstream{
			Tools:     pingTools,
			Intercept: serveOAuthMetadata,
			Authorize: func(r mcptest.Request) bool {
				authorized := r.Header.Get("Authorization") == "Bearer "+loginToken
				if authorized && r.Method == "initialize" {
					step.hold()
				}
				return authorized
			},
		})
	})
}

func holdNextTokenRequest(t *testing.T, tokens *authtest.TokenServer, step *heldStep) {
	// The token server closes its ready channel itself, so it gets its own; step.reached is
	// closed only through step.arrive.
	ready := make(chan struct{})
	tokens.Mu.Lock()
	tokens.HoldReady, tokens.HoldGate = ready, step.gate
	tokens.Mu.Unlock()
	go func() {
		select {
		case <-ready:
			step.arrive()
		case <-t.Context().Done():
		}
	}()
	t.Cleanup(step.release) // the token server waits on the gate alone, and closes before earlier cleanups
}

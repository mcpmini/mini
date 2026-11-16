//go:build test

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/testutil"
)

type loginUpstreamParams struct {
	Issuer string
	// Hold keeps every request with a token waiting until release is called.
	Hold bool
	// Fail answers every request with a token with a 400, so the reconnect after login fails.
	Fail bool
}

type loginUpstream struct {
	URL          string
	initializing chan struct{}
	release      func()
}

// newLoginUpstream is an OAuth-protected MCP server whose resource metadata names p.Issuer: it
// challenges requests without a token and serves a getData tool to those with one.
func newLoginUpstream(t *testing.T, p loginUpstreamParams) *loginUpstream {
	t.Helper()
	released := make(chan struct{})
	u := &loginUpstream{initializing: make(chan struct{}, 1), release: sync.OnceFunc(func() { close(released) })}
	if !p.Hold {
		u.release()
	}
	mcp := mcptest.NewHandler(mcptest.Upstream{Tools: []mcptest.Tool{{Name: "getData"}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			testutil.WriteJSON(w, http.StatusOK, authtest.ProtectedResource{
				Resource: "http://" + r.Host, AuthorizationServers: []string{p.Issuer},
			})
			return
		}
		if r.Header.Get("Authorization") == "" {
			authtest.Challenge{ResourceMetadata: "http://" + r.Host + "/.well-known/oauth-protected-resource"}.Write(w)
			return
		}
		select {
		case u.initializing <- struct{}{}:
		default:
		}
		<-released
		if p.Fail {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mcp.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { u.release(); srv.Close() })
	u.URL = srv.URL
	return u
}

type loginFixture struct {
	srv      *Server
	http     *httptest.Server
	handled  chan struct{}
	upstream *loginUpstream
	sc       config.ServerConfig
}

func newLoginFixture(t *testing.T, p loginUpstreamParams) *loginFixture {
	t.Helper()
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	srv.cfg.DisableAuthBrowserOpen = true
	tokenServer := authtest.NewTokenServer(t)
	p.Issuer = tokenServer.Srv.URL
	f := &loginFixture{srv: srv, handled: make(chan struct{}, 8), upstream: newLoginUpstream(t, p)}
	f.sc = config.ServerConfig{Name: "svc", Transport: "http", URL: f.upstream.URL, Auth: tokenServer.AuthConfig()}
	configtest.WriteServer(t, srv.configDir, f.sc)
	f.http = testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r)
		f.handled <- struct{}{}
	})
	return f
}

type loginStream struct {
	resp  *http.Response
	lines *json.Decoder
}

func (f *loginFixture) postLogin(ctx context.Context, t *testing.T, server string) *loginStream {
	t.Helper()
	body := bytes.NewReader([]byte(`{"server":"` + server + `"}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/login", body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() }) //nolint:errcheck // releases the stream; the test already read it
	return &loginStream{resp: resp, lines: json.NewDecoder(resp.Body)}
}

func (s *loginStream) next(t *testing.T) map[string]any {
	t.Helper()
	var line map[string]any
	if err := s.lines.Decode(&line); err != nil {
		t.Fatalf("read login line: %v", err)
	}
	return line
}

func (s *loginStream) authURL(t *testing.T) string {
	t.Helper()
	line := s.next(t)
	authURL, _ := line["url"].(string)
	if authURL == "" {
		t.Fatalf("first login line = %v, want the authorization URL", line)
	}
	return authURL
}

func denyAuthorization(t *testing.T, authURL string) {
	t.Helper()
	callback := authtest.LoopbackRedirectURI(t, authURL)
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	callback.RawQuery = url.Values{"error": {"access_denied"}, "state": {parsed.Query().Get("state")}}.Encode()
	if resp, err := http.Get(callback.String()); err == nil {
		resp.Body.Close() //nolint:errcheck // the denial is read from the login outcome, not this page
	}
}

func TestServeLogin_urlLineComesFirstAndOutcomeFollowsTheReconnect(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{Hold: true})
	stream := f.postLogin(t.Context(), t, "svc")
	authURL := stream.authURL(t)
	if got := stream.resp.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", got)
	}

	authtest.CompleteAuthorization(t, authURL, "test-auth-code")
	testutil.WaitForReceive(t, f.upstream.initializing, "the reconnect to reach the upstream")
	select {
	case <-authFlow(f.srv, "svc").done:
		t.Fatal("the login had an outcome while its reconnect was still blocked")
	default:
	}
	f.upstream.release()

	line := stream.next(t)
	if line["ok"] != true || line["expires"] == nil || line["connect_error"] != nil {
		t.Errorf("outcome = %v, want ok with an expiry and no connect error", line)
	}
	if !f.srv.isUpstreamRegistered("svc") {
		t.Error("svc isn't connected when the outcome arrives")
	}
}

func TestServeLogin_leavesTheBrowserClosedWhereStartAuthOpensIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: the browser command uses shell redirection")
	}
	f := newLoginFixture(t, loginUpstreamParams{})
	f.srv.cfg.DisableAuthBrowserOpen = false
	opened := filepath.Join(t.TempDir(), "opened")
	f.sc.Auth.BrowserCmd = `printf '%s\n' >> ` + opened
	configtest.WriteServer(t, f.srv.configDir, f.sc)

	f.postLogin(t.Context(), t, "svc").authURL(t)
	started, err := configureStartAuth(t.Context(), f.srv)
	if err != nil {
		t.Fatal(err)
	}

	agentURL, _ := started.(map[string]any)["url"].(string)
	if got := waitForOpenedURLs(t, opened); got != agentURL+"\n" {
		t.Errorf("browser opened %q, want only start_auth's URL %q", got, agentURL)
	}
}

func waitForOpenedURLs(t *testing.T, path string) string {
	t.Helper()
	for range 100 {
		data, err := os.ReadFile(path) //fileiolint:allow output of the browser process
		if err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the browser command never ran")
	return ""
}

func TestServeLogin_failedReconnectIsOKWithItsConnectError(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{Fail: true})
	stream := f.postLogin(t.Context(), t, "svc")

	authtest.CompleteAuthorization(t, stream.authURL(t), "test-auth-code")

	line := stream.next(t)
	if connectErr, _ := line["connect_error"].(string); line["ok"] != true || connectErr == "" {
		t.Errorf("outcome = %v, want ok with the reconnect's error", line)
	}
}

func TestServeLogin_deniedLoginIsAnAuthFlowError(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{})
	stream := f.postLogin(t.Context(), t, "svc")

	denyAuthorization(t, stream.authURL(t))

	line := stream.next(t)
	if message, _ := line["message"].(string); line["error"] == nil || !strings.HasPrefix(message, "auth flow: ") {
		t.Errorf("outcome = %v, want an auth flow error", line)
	}
}

func TestServeLogin_failedSaveIsASaveLoginError(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{})
	stream := f.postLogin(t.Context(), t, "svc")
	authURL := stream.authURL(t)
	testutil.WriteFile(
		t,
		filepath.Join(auth.TokenPath(f.srv.configDir, "svc"), "blocked"),
		"a directory at the token's path",
	)

	authtest.CompleteAuthorization(t, authURL, "test-auth-code")

	line := stream.next(t)
	if message, _ := line["message"].(string); line["error"] == nil || !strings.HasPrefix(message, "save login: ") {
		t.Errorf("outcome = %v, want a save login error", line)
	}
}

func TestServeLogin_endingTheRequestCancelsTheLoginAndReleasesItsPort(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{})
	ctx, cancel := context.WithCancel(t.Context())
	stream := f.postLogin(ctx, t, "svc")
	stream.authURL(t)
	flow := authFlow(f.srv, "svc")

	cancel()

	testutil.WaitForReceive(t, flow.done, "the login to end with its request")
	if flow.outcome.err == nil || !strings.Contains(flow.outcome.err.Error(), errLoginRequestEnded.Error()) {
		t.Errorf("outcome = %+v, want the login cancelled by its request ending", flow.outcome)
	}
	authtest.RequireCallbackPortReleased(t, flow.login)
}

func TestServeLogin_endingTheRequestAfterTheTokenIsSavedLetsTheReconnectFinish(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{Hold: true})
	ctx, cancel := context.WithCancel(t.Context())
	stream := f.postLogin(ctx, t, "svc")
	authtest.CompleteAuthorization(t, stream.authURL(t), "test-auth-code")
	testutil.WaitForReceive(t, f.upstream.initializing, "the reconnect, which follows the token save")
	flow := authFlow(f.srv, "svc")

	cancel()
	testutil.WaitForReceive(t, f.handled, "the login request to end")
	f.upstream.release()

	testutil.WaitForReceive(t, flow.done, "the reconnect to finish")
	if flow.outcome.err != nil || flow.outcome.connectErr != nil || !f.srv.isUpstreamRegistered("svc") {
		t.Errorf(
			"outcome = %+v, connected %v; want the reconnect to complete",
			flow.outcome,
			f.srv.isUpstreamRegistered("svc"),
		)
	}
}

func TestServeLogin_loginReplacedAfterItsTokenIsSavedIsOKWithoutAConnectError(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{Hold: true})
	stream := f.postLogin(t.Context(), t, "svc")
	authtest.CompleteAuthorization(t, stream.authURL(t), "test-auth-code")
	testutil.WaitForReceive(t, f.upstream.initializing, "the reconnect, which follows the token save")

	f.postLogin(t.Context(), t, "svc").authURL(t)

	if line := stream.next(t); line["ok"] != true || line["connect_error"] != nil {
		t.Errorf("outcome = %v, want ok with no connect error", line)
	}
}

func TestServeLogin_cancelledLoginReportsWhy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(t *testing.T, f *loginFixture)
		want   string
	}{
		{"another login", func(t *testing.T, f *loginFixture) {
			f.postLogin(t.Context(), t, "svc").authURL(t)
		}, "login cancelled: replaced by another login"},
		{"a settings change", func(t *testing.T, f *loginFixture) {
			edited := f.sc
			edited.Headers = map[string]string{"X-Edited": "1"}
			configtest.WriteServer(t, f.srv.configDir, edited)
			f.srv.applyServerFiles(t.Context())
		}, "login cancelled: the server's settings changed"},
		{"remove_server", func(t *testing.T, f *loginFixture) {
			if _, err := f.srv.removeServerFromAgent("svc"); err != nil {
				t.Fatal(err)
			}
		}, "login cancelled: the server was removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoginFixture(t, loginUpstreamParams{})
			recordLoadedServer(t, f.srv, "svc")
			stream := f.postLogin(t.Context(), t, "svc")
			stream.authURL(t)

			tc.cancel(t, f)

			if line := stream.next(t); line["message"] != tc.want {
				t.Errorf("outcome = %v, want %q", line, tc.want)
			}
		})
	}
}

func TestServeLogin_reloadMatchingTheLoginsSettingsDoesNotCancelIt(t *testing.T) {
	f := newLoginFixture(t, loginUpstreamParams{})
	before := f.sc
	before.Headers = map[string]string{"X-Before-Probe": "1"}
	f.srv.recordConfigServers([]config.ServerConfig{before})
	stream := f.postLogin(t.Context(), t, "svc")
	authURL := stream.authURL(t)

	f.srv.applyServerFiles(t.Context())
	authtest.CompleteAuthorization(t, authURL, "test-auth-code")

	if line := stream.next(t); line["ok"] != true {
		t.Errorf("outcome = %v, want the login to finish despite the reload", line)
	}
}

func recordLoadedServer(t *testing.T, srv *Server, name string) {
	t.Helper()
	sc, found, err := config.LookupServer(srv.configDir, name)
	if err != nil || !found {
		t.Fatalf("load %s: found=%v err=%v", name, found, err)
	}
	srv.recordConfigServers([]config.ServerConfig{sc})
}

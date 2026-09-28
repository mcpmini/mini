package auth_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func startLogin(t *testing.T, ac *config.AuthConfig) *auth.BrowserLogin {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	login, err := auth.StartBrowserLogin(ac, ln)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	t.Cleanup(func() { login.Close() }) //nolint:errcheck
	return login
}

func TestBrowserLogin_endToEnd(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	token := pkceToken(t, mock.AuthConfig())
	if token.AccessToken != "test-access-token" {
		t.Errorf("access token = %q, want %q", token.AccessToken, "test-access-token")
	}
	if token.RefreshToken != "test-refresh-token" {
		t.Errorf("refresh token = %q, want %q", token.RefreshToken, "test-refresh-token")
	}
	if token.Expiry.IsZero() {
		t.Error("expected non-zero expiry")
	}
}

func pkceToken(t *testing.T, ac *config.AuthConfig) *oauth2.Token {
	t.Helper()
	login := startLogin(t, ac)
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("browser login: %v", err)
	}
	return token
}

func TestTokenSaveLoad(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock.AuthConfig())
	if err := auth.Save(dir, "myserver", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := auth.Load(dir, "myserver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("loaded token = %q, want %q", loaded.AccessToken, token.AccessToken)
	}
	if !loaded.Valid() {
		t.Error("loaded token should be valid")
	}
}

func TestBrowserLogin_nonBlocking(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := startLogin(t, mock.AuthConfig())

	if login.AuthURL() == "" {
		t.Fatal("expected non-empty auth URL")
	}
	ctx, ctxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ctxCancel()

	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	token, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if token.AccessToken != "test-access-token" {
		t.Errorf("access token = %q, want %q", token.AccessToken, "test-access-token")
	}
}

func TestBrowserLogin_redirectURIUsesLocalhost(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := startLogin(t, mock.AuthConfig())

	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	redirectURI := parsed.Query().Get("redirect_uri")
	redirectParsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}
	if redirectParsed.Hostname() != "localhost" {
		t.Errorf("redirect_uri host = %q, want localhost", redirectParsed.Hostname())
	}
}

func TestIsNotFound(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "nonexistent")
	if !auth.IsNotFound(err) {
		t.Errorf("expected IsNotFound=true for missing token, got false (err: %v)", err)
	}
}

func TestSave_tokenFilePermissions(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "myserver", pkceToken(t, mock.AuthConfig())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertTokenFilesPrivate(t, dir+"/internal")
}

func TestSaveTightensExistingTokenPermissions(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/internal/myserver.token.json"
	if err := os.MkdirAll(dir+"/internal", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(dir, "myserver", &oauth2.Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("token permissions = %#o, want 0600", got)
	}
}

func TestSaveReplacesSymlinkInsteadOfFollowingIt(t *testing.T) {
	dir := t.TempDir()
	internal := dir + "/internal"
	if err := os.MkdirAll(internal, 0700); err != nil {
		t.Fatal(err)
	}
	target := dir + "/target"
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	path := internal + "/myserver.token.json"
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := auth.Save(dir, "myserver", &oauth2.Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unchanged" {
		t.Errorf("symlink target was overwritten: %q", got)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("token path remained a symlink")
	}
}

func assertTokenFilesPrivate(t *testing.T, tokensDir string) {
	t.Helper()
	entries, err := os.ReadDir(tokensDir)
	if err != nil {
		t.Fatalf("ReadDir tokens: %v", err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Stat %s: %v", e.Name(), err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("token file %s has world/group-readable permissions: %v", e.Name(), info.Mode().Perm())
		}
	}
}

func TestSave_invalidServerName(t *testing.T) {
	token := &oauth2.Token{AccessToken: "tok"}
	err := auth.Save(t.TempDir(), "bad name!", token)
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestLoad_invalidServerName(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "bad name!")
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestPKCECallback_emptyCodeRejected(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := startLogin(t, mock.AuthConfig())

	parsed, _ := url.Parse(login.AuthURL())
	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = url.Values{"state": {parsed.Query().Get("state")}}.Encode()

	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty code, got %d", resp.StatusCode)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ctxCancel()
	_, waitErr := login.Wait(ctx)
	if waitErr == nil {
		t.Error("Wait should not succeed after empty code")
	}
}

func TestPKCECallback_stateMismatchRejected(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := startLogin(t, mock.AuthConfig())

	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = url.Values{"code": {"real-code"}, "state": {"wrong-state"}}.Encode()

	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for state mismatch, got %d", resp.StatusCode)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ctxCancel()
	_, waitErr := login.Wait(ctx)
	if waitErr == nil {
		t.Error("Wait should not succeed after state mismatch")
	}
}

func TestLoopbackCallbackPath_consistent(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := startLogin(t, mock.AuthConfig())

	parsed, _ := url.Parse(login.AuthURL())
	redirectURI := parsed.Query().Get("redirect_uri")
	cbURL, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}

	if cbURL.Path != auth.LoopbackCallbackPath {
		t.Errorf("PKCE redirect_uri path = %q, want %q (must match Register's URI)", cbURL.Path, auth.LoopbackCallbackPath)
	}
}

func TestBuildAuthURL_extraParamsDoNotOverrideResource(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	ac := mock.AuthConfig()
	ac.ResourceURL = "https://resource.example.com"
	ac.ExtraAuthParams = map[string]string{
		"resource": "https://evil.example.com",
		"prompt":   "consent",
	}

	login := startLogin(t, ac)

	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := parsed.Query()
	if got := q.Get("resource"); got != "https://resource.example.com" {
		t.Errorf("resource = %q, want %q — ExtraAuthParams must not override computed resource", got, "https://resource.example.com")
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent — ExtraAuthParams should pass through", got)
	}
}

func TestTokenValidAfterForcedExpiry(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", pkceToken(t, mock.AuthConfig())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	auth.Save(dir, "srv", loaded) //nolint:errcheck
	reloaded, _ := auth.Load(dir, "srv")
	if reloaded.Valid() {
		t.Error("token should be invalid after forced expiry")
	}
}

func TestBrowserLogin_portReleasedAfterWait(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	login, err := auth.StartBrowserLogin(mock.AuthConfig(), ln)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	if _, err := login.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port not released after Wait: %v", err)
	}
	ln2.Close()
}

func TestBrowserLogin_portReleasedAfterClose(t *testing.T) {
	t.Run("immediate close", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		login, err := auth.StartBrowserLogin(&config.AuthConfig{}, ln)
		if err != nil {
			t.Fatalf("StartBrowserLogin: %v", err)
		}
		login.Close() //nolint:errcheck

		ln2, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port not released after Close: %v", err)
		}
		ln2.Close()
	})

	t.Run("close with pending wait", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		login, err := auth.StartBrowserLogin(&config.AuthConfig{}, ln)
		if err != nil {
			t.Fatalf("StartBrowserLogin: %v", err)
		}

		waitDone := make(chan error, 1)
		go func() {
			_, err := login.Wait(context.Background())
			waitDone <- err
		}()

		login.Close() //nolint:errcheck
		<-waitDone

		ln2, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port not released after Close with pending Wait: %v", err)
		}
		ln2.Close()
	})
}

func TestBrowserLogin_closeUnblocksWait(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	login, err := auth.StartBrowserLogin(&config.AuthConfig{}, ln)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		_, err := login.Wait(context.Background())
		waitDone <- err
	}()

	login.Close() //nolint:errcheck
	waitErr := <-waitDone

	if !errors.Is(waitErr, auth.ErrLoginClosed) {
		t.Errorf("Wait returned %v, want ErrLoginClosed", waitErr)
	}

	_, err2 := login.Wait(context.Background())
	if !errors.Is(err2, auth.ErrLoginClosed) {
		t.Errorf("second Wait returned %v, want ErrLoginClosed", err2)
	}

	login.Close() //nolint:errcheck
}

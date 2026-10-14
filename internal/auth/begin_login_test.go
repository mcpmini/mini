//go:build test

package auth_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestBeginLogin_completesWithoutWritingToTerminal(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	mock := authtest.NewTokenServer(t)
	sc := &config.ServerConfig{Name: "synthetic", URL: mock.Srv.URL + "/mcp", Auth: mock.AuthConfig()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var token string
	output := captureTerminalOutput(t, func() {
		login, err := auth.BeginLogin(ctx, *sc, beginLoginParams(t))
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		defer login.Close() //nolint:errcheck // Close always returns nil
		authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
		got, err := login.Wait(ctx)
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		token = got.Token.AccessToken
	})

	if token != "test-access-token" {
		t.Errorf("access token = %q, want test-access-token", token)
	}
	if output != "" {
		t.Errorf("login wrote to the terminal: %q", output)
	}
}

func TestBeginLogin_leavesTheCallersConfigAsConfigured(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	mock := authtest.NewTokenServer(t)
	sc := config.ServerConfig{Name: "synthetic", URL: mock.Srv.URL + "/mcp", Auth: mock.AuthConfig()}
	before := *sc.Auth.Clone()

	login, err := auth.BeginLogin(context.Background(), sc, beginLoginParams(t))
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	login.Close() //nolint:errcheck // Close always returns nil

	if !reflect.DeepEqual(*sc.Auth, before) {
		t.Errorf("BeginLogin changed the caller's auth config:\n got  %+v\n want %+v", *sc.Auth, before)
	}
}

func TestBeginLogin_persistsTheClientSelectedForTheExchange(t *testing.T) {
	auth.UseLoopbackEndpoints()
	auth.UseLoopbackHTTPClient()
	t.Cleanup(auth.ResetEndpointValidation)
	mock := authtest.NewTokenServer(t)
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           mock.Srv.URL + "/authorize",
		"token_endpoint":                   mock.Srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	dir := t.TempDir()
	wantClient := auth.Registration{
		ClientID: "selected-client", ClientSecret: "selected-secret", TokenEndpointAuthMethod: "client_secret_post",
	}
	if err := auth.SaveRegistration(dir, "synthetic", &wantClient); err != nil {
		t.Fatal(err)
	}
	sc := config.ServerConfig{
		Name: "synthetic", URL: as.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}
	login, err := auth.BeginLogin(
		context.Background(),
		sc,
		auth.BeginLoginParams{ConfigDir: dir, ServerName: sc.Name, Clock: clock.NewFake()},
	)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck // Close always returns nil
	if saveErr := auth.SaveRegistration(dir, sc.Name, &auth.Registration{
		ClientID:                wantClient.ClientID,
		ClientSecret:            "replacement-secret",
		TokenEndpointAuthMethod: "client_secret_basic",
	}); saveErr != nil {
		t.Fatal(saveErr)
	}
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	want := auth.Client{
		Source:     auth.ClientRegistered,
		ID:         wantClient.ClientID,
		Secret:     wantClient.ClientSecret,
		AuthMethod: wantClient.TokenEndpointAuthMethod,
	}
	if got.Client != want {
		t.Errorf("login client = %+v, want selected exchange client %+v", got.Client, want)
	}
	if got.TokenURL != mock.Srv.URL+"/token" {
		t.Errorf("login token URL = %q, want selected endpoint %q", got.TokenURL, mock.Srv.URL+"/token")
	}
	mock.Mu.Lock()
	defer mock.Mu.Unlock()
	if mock.LastSecret != wantClient.ClientSecret || mock.LastBasicAuth != "" || mock.LastBasicSecret != "" {
		t.Errorf(
			"exchange form secret = %q, basic user = %q, basic secret = %q; want selected post secret",
			mock.LastSecret,
			mock.LastBasicAuth,
			mock.LastBasicSecret,
		)
	}
}

func TestBeginLogin_keepsTheStartedClientWhenConfigChangesBeforeCallback(t *testing.T) {
	auth.UseLoopbackHTTPClient()
	mock := authtest.NewTokenServer(t)
	sc := config.ServerConfig{
		Name: "synthetic", URL: "https://resource.example.com/mcp",
		Auth: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, AuthURL: mock.Srv.URL + "/authorize", TokenURL: mock.Srv.URL + "/token",
			ClientID: "client-x", ClientSecret: "secret-x", TokenEndpointAuthMethod: "client_secret_post",
		},
	}
	login, err := auth.BeginLogin(
		context.Background(),
		sc,
		auth.BeginLoginParams{ConfigDir: t.TempDir(), ServerName: sc.Name},
	)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck // Close always returns nil
	sc.Auth.ClientID = "client-y"
	sc.Auth.ClientSecret = "secret-y"
	sc.Auth.TokenURL = "https://other.example.com/token"
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	want := auth.Client{Source: auth.ClientConfigured, ID: "client-x"}
	if got.Client != want || got.TokenURL != mock.Srv.URL+"/token" ||
		got.Resource != "https://resource.example.com/mcp" {
		t.Fatalf("login binding = %+v, want client X and its started endpoints/resource", got)
	}
	mock.Mu.Lock()
	defer mock.Mu.Unlock()
	if mock.LastClientID != "client-x" || mock.LastSecret != "secret-x" || mock.LastSecret == "secret-y" {
		t.Errorf("exchange used client=%q secret=%q, want client X secret", mock.LastClientID, mock.LastSecret)
	}
}

func TestBeginLogin_publicDCRDoesNotUseOrphanConfiguredSecret(t *testing.T) {
	auth.UseLoopbackEndpoints()
	auth.UseLoopbackHTTPClient()
	t.Cleanup(auth.ResetEndpointValidation)
	mock := authtest.NewTokenServer(t)
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           mock.Srv.URL + "/authorize",
		"token_endpoint":                   mock.Srv.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	dir := t.TempDir()
	if err := auth.SaveRegistration(
		dir,
		"synthetic",
		&auth.Registration{ClientID: "public-client", TokenEndpointAuthMethod: "none"},
	); err != nil {
		t.Fatal(err)
	}
	sc := config.ServerConfig{
		Name: "synthetic",
		URL:  as.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:                    config.AuthTypeOAuth2,
			ClientSecret:            "orphan-secret",
			TokenEndpointAuthMethod: "client_secret_post",
		},
	}
	login, err := auth.BeginLogin(
		context.Background(),
		sc,
		auth.BeginLoginParams{ConfigDir: dir, ServerName: sc.Name, Clock: clock.NewFake()},
	)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck // Close always returns nil
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Client != (auth.Client{Source: auth.ClientRegistered, ID: "public-client"}) {
		t.Errorf("login client = %+v, want public registered client without credentials", got.Client)
	}
	mock.Mu.Lock()
	defer mock.Mu.Unlock()
	if mock.LastSecret != "" || mock.LastBasicSecret != "" {
		t.Errorf("public exchange included a secret: form=%q basic=%q", mock.LastSecret, mock.LastBasicSecret)
	}
}

func TestBeginLogin_discoveryFailureReleasesCallbackPort(t *testing.T) {
	addr := freeLoopbackAddr(t)
	auth.UseCallbackListenAddr(addr)
	t.Cleanup(auth.UseEphemeralCallbackPort)
	noDiscovery := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(noDiscovery.Close)
	sc := &config.ServerConfig{
		Name: "synthetic",
		URL:  noDiscovery.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}

	login, err := auth.BeginLogin(context.Background(), *sc, beginLoginParams(t))

	if err == nil {
		login.Close() //nolint:errcheck // Close always returns nil
		t.Fatal("BeginLogin succeeded without discoverable endpoints")
	}
	if !strings.Contains(err.Error(), "resolve oauth endpoints") {
		t.Errorf("error = %v, want a resolve failure", err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("callback port still bound after failed BeginLogin: %v", err)
	}
	listener.Close() //nolint:errcheck // test listener only proves the port is free
}

func TestBrowserLogin_callbackServerFailureEndsWait(t *testing.T) {
	listener := newFailingListener(t)
	close(listener.fail)

	var waitErr error
	output := captureTerminalOutput(t, func() {
		login, err := auth.StartBrowserLogin(&config.AuthConfig{}, listener)
		if err != nil {
			t.Fatalf("StartBrowserLogin: %v", err)
		}
		defer login.Close() //nolint:errcheck // Close always returns nil
		waitErr = waitForLogin(t, login)
	})

	if waitErr == nil || !strings.Contains(waitErr.Error(), "oauth callback server: synthetic accept failure") {
		t.Errorf("Wait error = %v, want the callback server failure", waitErr)
	}
	if output != "" {
		t.Errorf("callback server failure wrote to the terminal: %q", output)
	}
}

func TestBrowserLogin_callbackThatArrivedBeforeServerFailureStillCompletes(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	listener := newFailingListener(t)
	ac := mock.AuthConfig()
	ac.ResourceURL = "https://resource.example.com"
	login, err := auth.StartBrowserLogin(ac, listener)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck // Close always returns nil

	auth.BufferCallbackCode(login, "test-auth-code")
	close(listener.fail)

	if err := waitForLogin(t, login); err != nil {
		t.Errorf("Wait = %v, want the buffered code to complete the login", err)
	}
}

func beginLoginParams(t *testing.T) auth.BeginLoginParams {
	return auth.BeginLoginParams{ConfigDir: t.TempDir(), ServerName: "synthetic", Clock: clock.NewFake()}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close() //nolint:errcheck // only the port number was needed
	return addr
}

func captureTerminalOutput(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, logOut := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w) // log keeps the writer it had at init, so swapping os.Stderr alone misses it
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() { io.Copy(&captured, r); close(copied) }() //nolint:errcheck // a short read only shrinks the capture
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		log.SetOutput(logOut)
	}()
	run()
	w.Close() //nolint:errcheck // closing the write end only ends the copy
	<-copied
	return captured.String()
}

type failingListener struct {
	net.Listener
	fail      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newFailingListener(t *testing.T) *failingListener {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return &failingListener{Listener: inner, fail: make(chan struct{}), closed: make(chan struct{})}
}

func (l *failingListener) Accept() (net.Conn, error) {
	select {
	case <-l.fail:
		return nil, errors.New("synthetic accept failure")
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *failingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close()
}
